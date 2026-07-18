package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	localereader "github.com/mattn/go-localereader"

	"github.com/panjie/mods/internal/platform"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/textutil"
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

	defaultShellProgressInterval = 2 * time.Second
	shellProgressTailLimit       = 4096
)

// ShellProgress describes a still-running shell command for UI status updates.
type ShellProgress struct {
	Tool       string
	Command    string
	Elapsed    time.Duration
	LastOutput string
}

// ShellProgressHandler receives best-effort progress updates while a shell
// command is still running. It must not affect command output or completion.
type ShellProgressHandler func(context.Context, ShellProgress)

// ShellConfig configures the native shell tool.
type ShellConfig struct {
	Root           string
	Timeout        time.Duration
	MaxOutputChars int
	SudoPrompt     SecretPromptHandler
	Progress       ShellProgressHandler
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
				"command":    stringProp("Shell command to run."),
				"secret_env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Environment variable names mapped to secret references returned by request_user_input."},
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command   string            `json:"command"`
				SecretEnv map[string]string `json:"secret_env"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if err := validateSecretEnv(args.SecretEnv); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, "shell_run", args.Command, args.SecretEnv, cfg.SudoPrompt, shellCommand)
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
				"command":    stringProp("PowerShell command to run directly."),
				"secret_env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Environment variable names mapped to secret references returned by request_user_input."},
			}, "command"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Command   string            `json:"command"`
				SecretEnv map[string]string `json:"secret_env"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if err := validateSecretEnv(args.SecretEnv); err != nil {
				return "", err
			}
			return runShellCommand(ctx, cfg, root, "powershell_run", args.Command, args.SecretEnv, nil, powerShellCommand)
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
	Root             string
	Tool             string
	Timeout          time.Duration
	MaxOutputChars   int
	BuildCommand     func(context.Context, string) *exec.Cmd
	Env              map[string]string
	Progress         ShellProgressHandler
	ProgressInterval time.Duration
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
	if len(r.Env) > 0 {
		cmd.Env = os.Environ()
		for key, value := range r.Env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
	}
	out := newCappedOutput(r.MaxOutputChars)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		return out.String(), err
	}
	started := time.Now()
	r.reportProgress(runCtx, command, started, out)

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- cmd.Wait()
	}()

	var tick <-chan time.Time
	var ticker *time.Ticker
	if r.Progress != nil {
		interval := r.ProgressInterval
		if interval <= 0 {
			interval = defaultShellProgressInterval
		}
		ticker = time.NewTicker(interval)
		tick = ticker.C
		defer ticker.Stop()
	}

	for {
		select {
		case err := <-waitCh:
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
		case <-tick:
			r.reportProgress(runCtx, command, started, out)
		}
	}
}

func (r ShellRunner) reportProgress(ctx context.Context, command string, started time.Time, out *cappedOutput) {
	if r.Progress == nil {
		return
	}
	r.Progress(ctx, ShellProgress{
		Tool:       r.Tool,
		Command:    command,
		Elapsed:    time.Since(started),
		LastOutput: out.LastLine(),
	})
}

func runShellCommand(ctx context.Context, cfg ShellConfig, root, toolName, command string, env map[string]string, sudoPrompt SecretPromptHandler, buildCmd func(context.Context, string) *exec.Cmd) (string, error) {
	prepared, cleanup, err := prepareSudoCommand(ctx, command, sudoPrompt)
	if err != nil {
		return "", err
	}
	defer cleanup()
	for key, value := range prepared.Env {
		if env == nil {
			env = map[string]string{}
		}
		env[key] = value
	}
	return ShellRunner{
		Root:           root,
		Tool:           toolName,
		Timeout:        cfg.Timeout,
		MaxOutputChars: cfg.MaxOutputChars,
		BuildCommand:   buildCmd,
		Env:            env,
		Progress:       cfg.Progress,
	}.Run(ctx, prepared.Command)
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validateSecretEnv(env map[string]string) error {
	for name := range env {
		if !envNamePattern.MatchString(name) {
			return fmt.Errorf("invalid environment variable name %q", name)
		}
	}
	return nil
}

type cappedOutput struct {
	mu        sync.Mutex
	buf       bytes.Buffer
	tail      []byte
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
	if len(p) >= shellProgressTailLimit {
		w.tail = append(w.tail[:0], p[len(p)-shellProgressTailLimit:]...)
	} else {
		w.tail = append(w.tail, p...)
	}
	if len(w.tail) > shellProgressTailLimit {
		w.tail = append([]byte(nil), w.tail[len(w.tail)-shellProgressTailLimit:]...)
	}
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
	out := append([]byte(nil), w.buf.Bytes()...)
	truncated := w.truncated
	limit := w.limit
	w.mu.Unlock()
	text := decodeOutput(out)
	text = textutil.ValidUTF8Prefix(text)
	if truncated {
		return text + fmt.Sprintf("\n\n[Output truncated at %d chars.]", limit)
	}
	return text
}

func (w *cappedOutput) LastLine() string {
	w.mu.Lock()
	out := append([]byte(nil), w.tail...)
	w.mu.Unlock()
	text := strings.ReplaceAll(decodeOutput(out), "\r", "\n")
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

func decodeOutput(out []byte) string {
	if decoded, decErr := localereader.UTF8(out); decErr == nil {
		out = decoded
	}
	return string(out)
}

func appendExitStatus(text string, code int) string {
	if text != "" {
		return text + fmt.Sprintf("\n[exit status %d]", code)
	}
	return fmt.Sprintf("[exit status %d]", code)
}

func shellCommand(ctx context.Context, command string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return powerShellCommand(ctx, command)
	}
	return exec.CommandContext(ctx, "sh", "-c", command)
}

func powerShellCommand(ctx context.Context, command string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, windowsPowerShellExe(), "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command)
	platform.HideCommandWindow(cmd)
	return cmd
}

var (
	winPwshExe     string
	winPwshExeOnce sync.Once
)

// windowsPowerShellExe resolves the PowerShell host that executes shell_run /
// powershell_run commands. PowerShell 7 (pwsh.exe) is preferred when on PATH:
// it ships Get-FileHash as a core cmdlet (scoop and other package managers
// rely on it for hash verification, and the PS5.1 -NoProfile auto-load timing
// is unreliable on some locales). When pwsh.exe is absent it falls back to
// Windows PowerShell 5.1 (powershell.exe). The classification bridge
// (internal/approval.getWindowsShellPath) must resolve to the same host so
// AST parsing matches execution semantics.
func windowsPowerShellExe() string {
	winPwshExeOnce.Do(func() {
		if p, err := exec.LookPath("pwsh.exe"); err == nil {
			winPwshExe = p
			return
		}
		if p, err := exec.LookPath("powershell.exe"); err == nil {
			winPwshExe = p
		}
	})
	return winPwshExe
}
