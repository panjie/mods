package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/proto"
)

const (
	defaultReadLimit     = 20000
	maxReadLimit         = 100000
	defaultSearchResults = 100
	maxSearchResults     = 500
	maxSearchFileBytes   = 1 << 20
)

// FilesystemConfig configures native filesystem tools.
type FilesystemConfig struct {
	Root     string
	SafeDirs []string
}

// RegisterFilesystem registers native filesystem tools.
func RegisterFilesystem(registry *Registry, cfg FilesystemConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			debug.Printf("RegisterFilesystem: workspace %q does not exist, skipping filesystem tools", root)
			return nil
		}
		return err
	}

	safeDirs := cfg.SafeDirs

	register := func(tool Tool) error {
		return registry.Register(tool)
	}

	if err := register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "fs_read_file",
			Description: "Read a UTF-8 text file from the workspace. Use offset and limit to read large files in chunks.",
			InputSchema: objectSchema(map[string]any{
				"path":   stringProp("Path to the file, relative to the workspace or absolute within it."),
				"offset": integerProp("Zero-based byte offset to start reading from."),
				"limit":  integerProp("Maximum bytes to return."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			if args.Offset < 0 {
				return "", fmt.Errorf("offset must be non-negative")
			}
			if int64(args.Offset) > info.Size() {
				return "", fmt.Errorf("offset %d is beyond file size %d", args.Offset, info.Size())
			}
			limit := args.Limit
			if limit <= 0 {
				limit = defaultReadLimit
			}
			if limit > maxReadLimit {
				limit = maxReadLimit
			}
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			defer file.Close() //nolint:errcheck
			if _, err := file.Seek(int64(args.Offset), io.SeekStart); err != nil {
				return "", err
			}
			content, err := io.ReadAll(io.LimitReader(file, int64(limit)))
			if err != nil {
				return "", err
			}
			end := args.Offset + len(content)
			out := string(content)
			if int64(end) < info.Size() {
				out += fmt.Sprintf("\n\n[Output truncated. Read from offset %d to continue.]", end)
			}
			return out, nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{Mutable: true},
		Spec: proto.ToolSpec{
			Name:        "fs_write_file",
			Description: "Write a UTF-8 text file inside the workspace, replacing existing content.",
			InputSchema: objectSchema(map[string]any{
				"path":    stringProp("Path to write, relative to the workspace or absolute within it."),
				"content": stringProp("Complete file content to write."),
			}, "path", "content"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return "", err
			}
			if err := os.WriteFile(path, []byte(args.Content), 0o644); err != nil {
				return "", err
			}
			return fmt.Sprintf("Wrote %d bytes to %s", len(args.Content), workspaceRel(root, path)), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "fs_list_dir",
			Description: "List files and directories in a workspace directory.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory path, relative to the workspace or absolute within it."),
				"max_entries": integerProp("Maximum entries to return."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				MaxEntries int    `json:"max_entries"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			limit := args.MaxEntries
			if limit <= 0 || limit > 1000 {
				limit = 1000
			}
			var sb strings.Builder
			for i, entry := range entries {
				if i >= limit {
					sb.WriteString(fmt.Sprintf("[Output truncated. %d entries omitted.]\n", len(entries)-i))
					break
				}
				kind := "file"
				if entry.IsDir() {
					kind = "dir"
				}
				sb.WriteString(fmt.Sprintf("%s\t%s\n", kind, entry.Name()))
			}
			return strings.TrimRight(sb.String(), "\n"), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "fs_stat",
			Description: "Get metadata for a workspace file or directory.",
			InputSchema: objectSchema(map[string]any{
				"path": stringProp("Path to inspect, relative to the workspace or absolute within it."),
			}, "path"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path string `json:"path"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			path, err := resolveWorkspacePath(root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			info, err := os.Stat(path)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("path: %s\nname: %s\nis_dir: %v\nsize: %d\nmode: %s\nmodified: %s",
				workspaceRel(root, path), info.Name(), info.IsDir(), info.Size(), info.Mode(), info.ModTime().Format(time.RFC3339)), nil
		},
	}); err != nil {
		return err
	}

	if err := register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "fs_search",
			Description: "Search text files in the workspace for a literal query string.",
			InputSchema: objectSchema(map[string]any{
				"path":        stringProp("Directory or file path to search within."),
				"query":       stringProp("Literal text to search for."),
				"max_results": integerProp("Maximum matching lines to return."),
			}, "path", "query"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Path       string `json:"path"`
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if args.Query == "" {
				return "", fmt.Errorf("query is required")
			}
			path, err := resolveWorkspacePath(root, args.Path, safeDirs)
			if err != nil {
				return "", err
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = defaultSearchResults
			}
			if limit > maxSearchResults {
				limit = maxSearchResults
			}
			return searchFiles(ctx, root, path, args.Query, limit)
		},
	}); err != nil {
		return err
	}

	return register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{Mutable: true},
		Spec: proto.ToolSpec{
			Name:        "fs_apply_patch",
			Description: "Apply a unified diff patch to files inside the workspace.",
			InputSchema: objectSchema(map[string]any{
				"patch": stringProp("Unified diff patch text."),
			}, "patch"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Patch string `json:"patch"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if strings.TrimSpace(args.Patch) == "" {
				return "", fmt.Errorf("patch is required")
			}
			if err := validatePatchPaths(root, args.Patch); err != nil {
				return "", err
			}
			cmd := exec.CommandContext(ctx, "git", "-c", "core.autocrlf=false", "apply", "--whitespace=nowarn")
			platform.HideCommandWindow(cmd)
			cmd.Dir = root
			cmd.Stdin = strings.NewReader(args.Patch)
			out, err := cmd.CombinedOutput()
			if err != nil {
				return "", fmt.Errorf("git apply failed: %w\n%s", err, strings.TrimSpace(string(out)))
			}
			return "Patch applied", nil
		},
	})
}

func searchFiles(ctx context.Context, root, path, query string, limit int) (string, error) {
	var sb strings.Builder
	matches := 0
	err := filepath.WalkDir(path, func(path string, d fs.DirEntry, err error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return err
		}
		if matches >= limit {
			return fs.SkipAll
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.Size() > maxSearchFileBytes {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close() //nolint:errcheck
		reader := bufio.NewReader(file)
		lineNo := 0
		for {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
			line, readErr := reader.ReadString('\n')
			if len(line) > 0 {
				lineNo++
				if strings.Contains(line, "\x00") {
					return nil
				}
				line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
				if strings.Contains(line, query) {
					sb.WriteString(fmt.Sprintf("%s:%d:%s\n", workspaceRel(root, path), lineNo, line))
					matches++
					if matches >= limit {
						return fs.SkipAll
					}
				}
			}
			if errors.Is(readErr, io.EOF) {
				break
			}
			if readErr != nil {
				return readErr
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if matches == 0 {
		return "No matches found", nil
	}
	return strings.TrimRight(sb.String(), "\n"), nil
}
