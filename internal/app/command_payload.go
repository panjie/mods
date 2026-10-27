package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"

	toolregistry "github.com/panjie/mods/internal/tools"
)

const (
	maxCommandPayloadBytes     = 256 * 1024
	commandPayloadPrepareLimit = 2 * time.Second
)

// A cancelled OS file operation may remain blocked in the kernel. Bound those
// workers too, so repeated preview timeouts cannot accumulate goroutines.
var commandPayloadReaders = make(chan struct{}, 2)

// commandPayload is a review snapshot, never authorization evidence. Execution
// continues to use the original path and argv; the digest check does not remove
// filesystem races or freeze imports, child processes, or other side effects.
type commandPayload struct {
	Interpreter string
	Argv        []string
	Cwd         string
	Path        string
	Size        int64
	Digest      string
	Encoding    string
	Source      string
	Complete    bool
	Unavailable string

	tool    string
	args    []byte
	binding toolregistry.ProcessProgramBinding
	info    os.FileInfo
	modTime time.Time
}

type commandPayloadArgs struct {
	Program string   `json:"program"`
	Args    []string `json:"args"`
}

func prepareCommandPayload(ctx context.Context, name string, data []byte, cwd string, binding toolregistry.ProcessProgramBinding) (*commandPayload, error) {
	if name != "process_run" {
		return nil, nil
	}
	var parsed commandPayloadArgs
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	interpreter := binding.Resolved
	if interpreter == "" {
		interpreter = binding.Requested
	}
	if interpreter == "" {
		interpreter = parsed.Program
	}
	script, ok := literalProcessScript(interpreter, parsed.Args)
	if !ok {
		return nil, nil
	}
	if runtime.GOOS == "windows" && !filepath.IsAbs(script) && (filepath.VolumeName(script) != "" || os.IsPathSeparator(script[0])) {
		// Drive-relative and rooted-without-volume paths need interpreter-specific
		// resolution. Keep the ordinary unknown-program review for these forms.
		return nil, nil
	}
	parent := ctx
	ctx, cancel := context.WithTimeout(ctx, commandPayloadPrepareLimit)
	defer cancel()
	// process_run passes literal argv. Shell expansion would preview a different
	// file for names containing $, ~, quotes, or surrounding whitespace.
	path := script
	if !filepath.IsAbs(path) {
		// Do not clean link/../file: the OS resolves .. after following links.
		path = cwd + string(os.PathSeparator) + path
	}
	payload := &commandPayload{
		Interpreter: interpreter,
		Argv:        append([]string(nil), parsed.Args...),
		Cwd:         cwd,
		Path:        path,
		tool:        name,
		args:        append([]byte(nil), data...),
		binding:     binding,
	}
	info, content, err := readCommandPayloadFile(ctx, path)
	if err != nil {
		if parent.Err() != nil {
			return nil, parent.Err()
		}
		payload.Unavailable = "Script cannot be read: " + err.Error() + "."
		return payload, nil
	}
	payload.info, payload.modTime, payload.Size = info, info.ModTime(), info.Size()
	if len(content) > maxCommandPayloadBytes {
		payload.Unavailable = "Script exceeds the 256 KiB limit."
		return payload, nil
	}
	digest := sha256.Sum256(content)
	payload.Digest = hex.EncodeToString(digest[:])
	switch {
	case bytes.HasPrefix(content, []byte{0xff, 0xfe}) || bytes.HasPrefix(content, []byte{0xfe, 0xff}):
		payload.Unavailable = "UTF-16 script preview is not supported."
		return payload, nil
	case !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0:
		payload.Unavailable = "Script contains binary data or is not valid UTF-8."
		return payload, nil
	}
	payload.Complete = true
	payload.Encoding = "UTF-8"
	payload.Source = strings.TrimPrefix(string(content), "\ufeff")
	if strings.HasPrefix(string(content), "\ufeff") {
		payload.Encoding = "UTF-8 with BOM"
	}
	return payload, nil
}

func literalProcessScript(program string, args []string) (string, bool) {
	name := strings.ToLower(filepath.Base(program))
	name = strings.TrimSuffix(name, ".exe")
	if name == "powershell" || name == "pwsh" {
		for i := 0; i < len(args); i++ {
			switch strings.ToLower(args[i]) {
			case "-noprofile", "-noninteractive", "-nologo":
			case "-file":
				if i+1 < len(args) && args[i+1] != "" && args[i+1] != "-" {
					return args[i+1], true
				}
				return "", false
			default:
				return "", false
			}
		}
		return "", false
	}
	switch name {
	case "python", "python3", "py", "node", "ruby", "perl", "sh", "bash", "zsh", "ksh":
	default:
		return "", false
	}
	if len(args) == 0 || args[0] == "" || strings.HasPrefix(args[0], "-") {
		return "", false
	}
	return args[0], true
}

func readCommandPayloadFile(ctx context.Context, path string) (os.FileInfo, []byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case commandPayloadReaders <- struct{}{}:
	default:
		return nil, nil, fmt.Errorf("script preview readers are busy")
	}
	type result struct {
		info    os.FileInfo
		content []byte
		err     error
	}
	resultCh := make(chan result, 1)
	go func() {
		defer func() { <-commandPayloadReaders }()
		// Reject devices and FIFOs before opening. This is resource protection,
		// not a claim that path replacement races are eliminated.
		before, err := os.Stat(path)
		if err == nil && !before.Mode().IsRegular() {
			err = fmt.Errorf("not a regular file")
		}
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		file, err := os.Open(path)
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		defer file.Close()
		info, err := file.Stat()
		if err == nil && !info.Mode().IsRegular() {
			err = fmt.Errorf("not a regular file")
		}
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		content, err := io.ReadAll(io.LimitReader(file, maxCommandPayloadBytes+1))
		if err != nil {
			resultCh <- result{err: err}
			return
		}
		finalInfo, err := file.Stat()
		if err == nil && (!os.SameFile(before, finalInfo) || !os.SameFile(info, finalInfo) || info.Size() != finalInfo.Size() || !info.ModTime().Equal(finalInfo.ModTime())) {
			err = fmt.Errorf("file changed while reading")
		}
		resultCh <- result{info: finalInfo, content: content, err: err}
	}()
	select {
	case <-ctx.Done():
		return nil, nil, ctx.Err()
	case result := <-resultCh:
		return result.info, result.content, result.err
	}
}

func (p *commandPayload) reviewRows(redact func(string) string) []interactionRow {
	if p == nil {
		return nil
	}
	red := func(value string) string {
		if redact != nil {
			value = redact(value)
		}
		return redactRemoteURLsForDisplay(value)
	}
	rows := []interactionRow{{Label: "Script path", Value: red(p.Path)}}
	if !p.Complete {
		return append(rows,
			interactionRow{Label: "Script preview", Value: red(p.Unavailable + " Full script content was not reviewed; execution keeps the original path and argv.")},
			interactionRow{Label: "Script provenance", Value: "Unknown; this is not a complete-payload review."},
		)
	}
	return append(rows,
		interactionRow{Label: "Script preview", Value: fmt.Sprintf("Complete %s source · %d bytes · SHA-256 %s · rechecked before execution; imports and filesystem races remain outside this snapshot.", p.Encoding, p.Size, p.Digest)},
		interactionRow{Label: "Script source", Value: numberedReviewSource(red(p.Source))},
		interactionRow{Label: "Script provenance", Value: "Unknown; preview and digest are review aids, not permission evidence."},
	)
}

func (p *commandPayload) revalidate(ctx context.Context) (bool, error) {
	if p == nil {
		return false, nil
	}
	fresh, err := prepareCommandPayload(ctx, p.tool, p.args, p.Cwd, p.binding)
	if err != nil {
		return false, err
	}
	if fresh == nil {
		return true, nil
	}
	changed := p.Path != fresh.Path || p.Cwd != fresh.Cwd || p.Interpreter != fresh.Interpreter ||
		p.Size != fresh.Size || p.Digest != fresh.Digest || p.Encoding != fresh.Encoding ||
		p.Source != fresh.Source || p.Complete != fresh.Complete || p.Unavailable != fresh.Unavailable ||
		!bytes.Equal(p.args, fresh.args) || !p.modTime.Equal(fresh.modTime)
	if (p.info == nil) != (fresh.info == nil) {
		changed = true
	} else if p.info != nil && !os.SameFile(p.info, fresh.info) {
		changed = true
	}
	return changed, nil
}
