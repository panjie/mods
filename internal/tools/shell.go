package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	localereader "github.com/mattn/go-localereader"

	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/proto"
)

const (
	// DefaultShellTimeout is the canonical default timeout for the native
	// shell tool when BuiltinToolsConfig.ShellTimeout is unset. The config
	// layer references this constant so there is a single source of truth.
	DefaultShellTimeout = 30 * time.Second
	// DefaultShellMaxOutput is the canonical default output cap for the
	// native shell tool when BuiltinToolsConfig.ShellMaxOutput is unset.
	DefaultShellMaxOutput = 20000

	// defaultShellTimeout / defaultShellOutput are aliases kept so the
	// in-file references read naturally; they mirror the exported names
	// above so external callers and internal usage cannot drift.
	defaultShellTimeout = DefaultShellTimeout
	defaultShellOutput  = DefaultShellMaxOutput
)

// ShellConfig configures the native shell tool.
type ShellConfig struct {
	Root           string
	Timeout        time.Duration
	MaxOutputChars int
}

// RegisterShell registers the native shell tool.
func RegisterShell(registry *Registry, cfg ShellConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultShellTimeout
	}
	if cfg.MaxOutputChars <= 0 {
		cfg.MaxOutputChars = defaultShellOutput
	}
	desc := PosixShellRunDescription
	if runtime.GOOS == "windows" {
		desc = WindowsShellRunDescription
	}
	return registry.Register(Tool{
		Kind:          ToolKindShell,
		TimeoutPolicy: TimeoutPolicySelf,
		Capabilities:  ToolCapabilities{Mutable: true, ShellExecution: true},
		Spec: proto.ToolSpec{
			Name:        "shell_run",
			Description: desc,
			InputSchema: objectSchema(map[string]any{
				"command": stringProp("Shell command to run."),
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, args.Command, shellCommand)
		},
	})
}

// RegisterPowerShell registers the native PowerShell tool.
func RegisterPowerShell(registry *Registry, cfg ShellConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultShellTimeout
	}
	if cfg.MaxOutputChars <= 0 {
		cfg.MaxOutputChars = defaultShellOutput
	}
	return registry.Register(Tool{
		Kind:          ToolKindShell,
		TimeoutPolicy: TimeoutPolicySelf,
		Capabilities:  ToolCapabilities{Mutable: true, ShellExecution: true},
		Spec: proto.ToolSpec{
			Name:        "powershell_run",
			Description: PowerShellRunDescription,
			InputSchema: objectSchema(map[string]any{
				"command": stringProp("PowerShell command to run directly."),
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command string `json:"command"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, args.Command, powerShellCommand)
		},
	})
}

type ShellExitError struct {
	Code int
}

func (e ShellExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.Code)
}

// ExitCode returns the process exit status carried by the error.
func (e ShellExitError) ExitCode() int {
	return e.Code
}

type ShellRunner struct {
	Root           string
	Timeout        time.Duration
	MaxOutputChars int
	BuildCommand   func(context.Context, string) *exec.Cmd
}

func (r ShellRunner) Run(ctx context.Context, command string) (string, error) {
	if strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("command is required")
	}
	if r.Timeout <= 0 {
		r.Timeout = defaultShellTimeout
	}
	if r.MaxOutputChars <= 0 {
		r.MaxOutputChars = defaultShellOutput
	}
	if r.BuildCommand == nil {
		return "", fmt.Errorf("shell runner command builder is required")
	}
	runCtx, cancel := context.WithTimeout(ctx, r.Timeout)
	defer cancel()
	cmd := r.BuildCommand(runCtx, command)
	cmd.Dir = r.Root
	out := newCappedOutput(r.MaxOutputChars)
	cmd.Stdout = out
	cmd.Stderr = out
	err := cmd.Run()
	text := out.String()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			text = appendExitStatus(text, exitErr.ExitCode())
			return text, ShellExitError{Code: exitErr.ExitCode()}
		}
		return text, err
	}
	return text, nil
}

func runShellCommand(ctx context.Context, cfg ShellConfig, root, command string, buildCmd func(context.Context, string) *exec.Cmd) (string, error) {
	return ShellRunner{
		Root:           root,
		Timeout:        cfg.Timeout,
		MaxOutputChars: cfg.MaxOutputChars,
		BuildCommand:   buildCmd,
	}.Run(ctx, command)
}

type cappedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func newCappedOutput(limit int) *cappedOutput {
	if limit <= 0 {
		limit = defaultShellOutput
	}
	return &cappedOutput{limit: limit}
}

func (w *cappedOutput) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.limit - w.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			w.buf.Write(p[:remaining])
			w.truncated = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.truncated = true
	}
	return len(p), nil
}

func (w *cappedOutput) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.buf.Bytes()
	if decoded, decErr := localereader.UTF8(out); decErr == nil {
		out = decoded
	}
	text := string(out)
	if w.truncated {
		return text + fmt.Sprintf("\n\n[Output truncated at %d chars.]", w.limit)
	}
	return text
}

func appendExitStatus(text string, code int) string {
	if text != "" {
		return text + fmt.Sprintf("\n[exit status %d]", code)
	}
	return fmt.Sprintf("[exit status %d]", code)
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		cmd := exec.CommandContext(ctx, "cmd", "/D", "/C", command)
		platform.HideCommandWindow(cmd)
		return cmd
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func powerShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	platform.HideCommandWindow(cmd)
	return cmd
}
