package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	localereader "github.com/mattn/go-localereader"
)

const (
	commandOutputLimit = 32 << 10
	processKillGrace   = 500 * time.Millisecond
)

type commandRunResult struct {
	ExitCode  *int
	Duration  time.Duration
	TimedOut  bool
	WaitError error
}

type commandRunner struct {
	Parent       context.Context
	Timeout      time.Duration
	BuildCommand func(context.Context) *exec.Cmd
	Stdout       io.Writer
	Stderr       io.Writer
	Progress     func(time.Duration)
	Interval     time.Duration
}

func (r commandRunner) Run() (commandRunResult, error) {
	if r.Parent == nil {
		r.Parent = context.Background()
	}
	if r.Timeout <= 0 {
		r.Timeout = defaultShellTimeout
	}
	if r.BuildCommand == nil {
		return commandRunResult{}, fmt.Errorf("command builder is required")
	}
	runCtx, cancel := context.WithTimeout(r.Parent, r.Timeout)
	defer cancel()

	cmd := r.BuildCommand(runCtx)
	if cmd == nil {
		return commandRunResult{}, fmt.Errorf("command builder returned nil")
	}
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	cmd.WaitDelay = processKillGrace
	tree, err := newProcessTree(cmd, processKillGrace)
	if err != nil {
		return commandRunResult{}, err
	}
	defer tree.Close()

	if err := cmd.Start(); err != nil {
		return commandRunResult{}, err
	}
	if err := tree.Attach(cmd); err != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return commandRunResult{}, fmt.Errorf("could not manage process tree: %w", err)
	}
	started := time.Now()
	if r.Progress != nil {
		r.Progress(0)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	var tick <-chan time.Time
	var ticker *time.Ticker
	if r.Progress != nil {
		interval := r.Interval
		if interval <= 0 {
			interval = defaultShellProgressInterval
		}
		ticker = time.NewTicker(interval)
		tick = ticker.C
		defer ticker.Stop()
	}

	var waitErr error
	for {
		select {
		case waitErr = <-waitCh:
			goto finished
		case <-tick:
			r.Progress(time.Since(started))
		}
	}

finished:
	duration := time.Since(started)
	if r.Parent.Err() != nil {
		return commandRunResult{}, r.Parent.Err()
	}
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded)
	result := commandRunResult{Duration: duration, TimedOut: timedOut, WaitError: waitErr}
	if !timedOut && cmd.ProcessState != nil {
		code := cmd.ProcessState.ExitCode()
		if code >= 0 {
			result.ExitCode = &code
		}
	}
	return result, nil
}

type boundedOutput struct {
	mu        sync.Mutex
	limit     int
	headLimit int
	tailLimit int
	total     int64
	head      []byte
	tail      []byte
}

type boundedOutputSnapshot struct {
	Text         string
	Truncated    bool
	OmittedBytes int64
}

func newBoundedOutput(limit int) *boundedOutput {
	if limit <= 0 {
		limit = commandOutputLimit
	}
	headLimit := limit / 2
	return &boundedOutput{limit: limit, headLimit: headLimit, tailLimit: limit - headLimit}
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	original := len(p)
	w.total += int64(original)
	if remaining := w.headLimit - len(w.head); remaining > 0 {
		take := min(remaining, len(p))
		w.head = append(w.head, p[:take]...)
		p = p[take:]
	}
	if len(p) > 0 {
		w.tail = append(w.tail, p...)
		if len(w.tail) > w.tailLimit {
			w.tail = append([]byte(nil), w.tail[len(w.tail)-w.tailLimit:]...)
		}
	}
	return original, nil
}

func (w *boundedOutput) Snapshot() boundedOutputSnapshot {
	w.mu.Lock()
	head := append([]byte(nil), w.head...)
	tail := append([]byte(nil), w.tail...)
	total := w.total
	w.mu.Unlock()

	captured := int64(len(head) + len(tail))
	if total <= captured {
		return boundedOutputSnapshot{Text: decodeCommandOutput(append(head, tail...))}
	}
	omitted := total - captured
	marker := fmt.Sprintf("\n...[truncated %d bytes]...\n", omitted)
	return boundedOutputSnapshot{
		Text:         decodeCommandOutput(head) + marker + decodeCommandOutput(tail),
		Truncated:    true,
		OmittedBytes: omitted,
	}
}

func (w *boundedOutput) LastLine() string {
	w.mu.Lock()
	tail := append([]byte(nil), w.tail...)
	if len(tail) == 0 {
		tail = append(tail, w.head...)
	}
	w.mu.Unlock()
	text := strings.ReplaceAll(decodeCommandOutput(tail), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func decodeCommandOutput(out []byte) string {
	if !utf8.Valid(out) {
		if decoded, err := localereader.UTF8(out); err == nil {
			out = decoded
		}
	}
	return strings.ToValidUTF8(string(bytes.Clone(out)), "")
}
