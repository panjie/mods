package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/proto"
)

const maxRuntimeInfoCommands = 20

var runtimeCommandNamePattern = regexp.MustCompile(`^[A-Za-z0-9._+@%-]+$`)

type ProcessConfig struct {
	Root     string
	SafeDirs []string
	Timeout  time.Duration
	Progress ShellProgressHandler
}

type processRunArgs struct {
	Program   string            `json:"program"`
	Args      []string          `json:"args"`
	Cwd       string            `json:"cwd"`
	TimeoutMS *int64            `json:"timeout_ms"`
	SecretEnv map[string]string `json:"secret_env"`
}

type processRunResult struct {
	ExitCode        *int   `json:"exit_code"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	DurationMS      int64  `json:"duration_ms"`
	TimedOut        bool   `json:"timed_out"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StdoutOmitted   int64  `json:"stdout_omitted_bytes"`
	StderrTruncated bool   `json:"stderr_truncated"`
	StderrOmitted   int64  `json:"stderr_omitted_bytes"`
}

// ProcessProgramBinding pins the PATH resolution used during approval to the
// executable used by the eventual tool call.
type ProcessProgramBinding struct {
	Requested string
	Resolved  string
}

type processProgramBindingKey struct{}

// PrepareProcessProgram validates the process program before review and, for a
// bare name, resolves it exactly once so approval and execution cannot observe
// different PATH entries.
func PrepareProcessProgram(data []byte) (ProcessProgramBinding, error) {
	var args processRunArgs
	if err := decodeArgs(data, &args); err != nil {
		return ProcessProgramBinding{}, err
	}
	program := strings.TrimSpace(args.Program)
	if program == "" {
		return ProcessProgramBinding{}, fmt.Errorf("program is required")
	}
	if strings.IndexByte(program, 0) >= 0 {
		return ProcessProgramBinding{}, fmt.Errorf("program contains null byte")
	}
	binding := ProcessProgramBinding{Requested: program}
	if filepath.IsAbs(program) || strings.ContainsAny(program, `/\`) {
		if unsupportedProcessProgramForGOOS(program, runtime.GOOS) {
			return ProcessProgramBinding{}, unsupportedProcessProgramError(program)
		}
		return binding, nil
	}
	resolved, err := exec.LookPath(program)
	if err != nil {
		return ProcessProgramBinding{}, fmt.Errorf("program %q not found on PATH: %w", program, err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return ProcessProgramBinding{}, fmt.Errorf("resolve program %q: %w", program, err)
	}
	if unsupportedProcessProgramForGOOS(resolved, runtime.GOOS) {
		return ProcessProgramBinding{}, unsupportedProcessProgramError(resolved)
	}
	binding.Resolved = resolved
	return binding, nil
}

// WithProcessProgramBinding attaches a prepared process binding to one tool
// call. An empty resolved path means the caller supplied an explicit path that
// still needs ordinary workspace/authorization resolution in runProcess.
func WithProcessProgramBinding(ctx context.Context, binding ProcessProgramBinding) context.Context {
	if binding.Resolved == "" {
		return ctx
	}
	return context.WithValue(ctx, processProgramBindingKey{}, binding)
}

func processProgramBinding(ctx context.Context, requested string) (ProcessProgramBinding, bool) {
	binding, ok := ctx.Value(processProgramBindingKey{}).(ProcessProgramBinding)
	return binding, ok && binding.Requested == requested && binding.Resolved != ""
}

func unsupportedProcessProgramForGOOS(program, goos string) bool {
	if goos != "windows" {
		return false
	}
	switch strings.ToLower(filepath.Ext(program)) {
	case ".bat", ".cmd":
		return true
	default:
		return false
	}
}

func unsupportedProcessProgramError(program string) error {
	return fmt.Errorf("program %q is a Windows batch file; process_run cannot preserve literal argv for .bat or .cmd files, so use powershell_run with an explicitly reviewed command", program)
}

func RegisterProcess(registry *Registry, cfg ProcessConfig) error {
	root, err := filepath.Abs(cfg.Root)
	if err != nil {
		return err
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultShellTimeout
	}
	return registry.Register(Tool{
		Kind:          ToolKindShell,
		TimeoutPolicy: TimeoutPolicySelf,
		Capabilities:  ToolCapabilities{Mutable: true, ShellExecution: true},
		Spec: proto.ToolSpec{
			Name:        "process_run",
			Description: ProcessRunDescription,
			InputSchema: objectSchema(map[string]any{
				"program":    stringProp("Executable name or path. Shell builtins, pipelines, redirection, globbing, and variable expansion are not supported."),
				"args":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Literal argument vector. Each item is passed as one argument without shell parsing."},
				"cwd":        stringProp("Optional working directory. Defaults to the configured workspace; relative paths resolve from that workspace."),
				"timeout_ms": integerProp("Optional positive timeout in milliseconds; overrides the configured default (builtin-tools.shell-timeout) and may be larger or smaller than it."),
				"secret_env": map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Environment variable names mapped to secret references returned by request_user_input."},
			}, "program"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args processRunArgs
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return runProcess(ctx, cfg, root, args)
		},
	})
}

func runProcess(ctx context.Context, cfg ProcessConfig, root string, args processRunArgs) (string, error) {
	args.Program = strings.TrimSpace(args.Program)
	if args.Program == "" {
		return "", fmt.Errorf("program is required")
	}
	if strings.IndexByte(args.Program, 0) >= 0 {
		return "", fmt.Errorf("program contains null byte")
	}
	for i, arg := range args.Args {
		if strings.IndexByte(arg, 0) >= 0 {
			return "", fmt.Errorf("args[%d] contains null byte", i)
		}
	}
	if err := validateSecretEnv(args.SecretEnv); err != nil {
		return "", err
	}

	timeout, err := resolveCallTimeout(cfg.Timeout, args.TimeoutMS)
	if err != nil {
		return "", err
	}

	cwd := root
	if strings.TrimSpace(args.Cwd) != "" {
		var err error
		cwd, err = resolveWorkspacePath(ctx, root, args.Cwd, cfg.SafeDirs)
		if err != nil {
			return "", err
		}
	}
	info, err := os.Stat(cwd)
	if err != nil {
		return "", fmt.Errorf("could not use cwd %q: %w", args.Cwd, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cwd %q is not a directory", args.Cwd)
	}

	program, err := resolveProcessProgram(ctx, root, cwd, args.Program, cfg.SafeDirs)
	if err != nil {
		return "", err
	}
	stdout := newBoundedOutput(commandOutputLimit)
	stderr := newBoundedOutput(commandOutputLimit)
	display := FormatProcessInvocation(args.Program, args.Args)
	runResult, err := (commandRunner{
		Parent:  ctx,
		Timeout: timeout,
		BuildCommand: func(runCtx context.Context) *exec.Cmd {
			cmd := exec.CommandContext(runCtx, program, args.Args...) //nolint:gosec
			cmd.Dir = cwd
			if len(args.SecretEnv) > 0 {
				cmd.Env = os.Environ()
				for key, value := range args.SecretEnv {
					cmd.Env = append(cmd.Env, key+"="+value)
				}
			}
			return cmd
		},
		Stdout: stdout,
		Stderr: stderr,
		Progress: func(elapsed time.Duration) {
			if cfg.Progress == nil {
				return
			}
			last := stderr.LastLine()
			if last == "" {
				last = stdout.LastLine()
			}
			cfg.Progress(ctx, ShellProgress{Tool: "process_run", Command: display, Elapsed: elapsed, LastOutput: last})
		},
	}).Run()
	if err != nil {
		return "", err
	}
	if runResult.WaitError != nil && !runResult.TimedOut {
		var exitErr *exec.ExitError
		if !errors.As(runResult.WaitError, &exitErr) {
			return "", runResult.WaitError
		}
	}
	outSnapshot := stdout.Snapshot()
	errSnapshot := stderr.Snapshot()
	result := processRunResult{
		ExitCode:        runResult.ExitCode,
		Stdout:          outSnapshot.Text,
		Stderr:          errSnapshot.Text,
		DurationMS:      runResult.Duration.Milliseconds(),
		TimedOut:        runResult.TimedOut,
		StdoutTruncated: outSnapshot.Truncated,
		StdoutOmitted:   outSnapshot.OmittedBytes,
		StderrTruncated: errSnapshot.Truncated,
		StderrOmitted:   errSnapshot.OmittedBytes,
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func resolveProcessProgram(ctx context.Context, root, cwd, program string, safeDirs []string) (string, error) {
	if binding, ok := processProgramBinding(ctx, program); ok {
		if unsupportedProcessProgramForGOOS(binding.Resolved, runtime.GOOS) {
			return "", unsupportedProcessProgramError(binding.Resolved)
		}
		info, err := os.Stat(binding.Resolved)
		if err != nil {
			return "", fmt.Errorf("could not execute %q: %w", program, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("program %q is a directory", program)
		}
		return binding.Resolved, nil
	}
	if filepath.IsAbs(program) || strings.ContainsAny(program, `/\`) {
		input := program
		if !filepath.IsAbs(input) {
			input = filepath.Join(cwd, input)
		}
		resolved, err := resolveWorkspacePath(ctx, root, input, safeDirs)
		if err != nil {
			return "", err
		}
		info, err := os.Stat(resolved)
		if err != nil {
			return "", fmt.Errorf("could not execute %q: %w", program, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("program %q is a directory", program)
		}
		if unsupportedProcessProgramForGOOS(resolved, runtime.GOOS) {
			return "", unsupportedProcessProgramError(resolved)
		}
		return resolved, nil
	}
	resolved, err := exec.LookPath(program)
	if err != nil {
		return "", fmt.Errorf("program %q not found on PATH: %w", program, err)
	}
	if unsupportedProcessProgramForGOOS(resolved, runtime.GOOS) {
		return "", unsupportedProcessProgramError(resolved)
	}
	return resolved, nil
}

func FormatProcessInvocation(program string, args []string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, quoteProcessDisplayArg(program))
	for _, arg := range args {
		parts = append(parts, quoteProcessDisplayArg(arg))
	}
	return strings.Join(parts, " ")
}

func quoteProcessDisplayArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\r\n\"'") {
		return arg
	}
	return strconv.Quote(arg)
}

type RuntimeShellInfo struct {
	Dialect    string  `json:"dialect"`
	Executable string  `json:"executable"`
	Version    *string `json:"version"`
}

type runtimeCommandInfo struct {
	Found bool   `json:"found"`
	Path  string `json:"path,omitempty"`
}

type runtimeInfoResult struct {
	OS        string                        `json:"os"`
	Arch      string                        `json:"arch"`
	Workspace string                        `json:"workspace"`
	Shell     RuntimeShellInfo              `json:"shell"`
	Commands  map[string]runtimeCommandInfo `json:"commands,omitempty"`
}

func RegisterRuntimeInfo(registry *Registry, root string) error {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "runtime_info",
			Description: RuntimeInfoDescription,
			InputSchema: objectSchema(map[string]any{
				"commands": map[string]any{"type": "array", "maxItems": maxRuntimeInfoCommands, "items": map[string]any{"type": "string"}, "description": "Optional bare command names to resolve on PATH without executing them."},
			}),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Commands []string `json:"commands"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if len(args.Commands) > maxRuntimeInfoCommands {
				return "", fmt.Errorf("commands must contain at most %d names", maxRuntimeInfoCommands)
			}
			result := runtimeInfoResult{OS: runtime.GOOS, Arch: runtime.GOARCH, Workspace: absRoot, Shell: SelectedShellInfo()}
			if len(args.Commands) > 0 {
				result.Commands = make(map[string]runtimeCommandInfo)
			}
			for _, name := range args.Commands {
				if !runtimeCommandNamePattern.MatchString(name) {
					return "", fmt.Errorf("invalid command name %q: use a bare executable name without paths or shell syntax", name)
				}
				if _, exists := result.Commands[name]; exists {
					continue
				}
				path, err := exec.LookPath(name)
				result.Commands[name] = runtimeCommandInfo{Found: err == nil, Path: path}
			}
			encoded, err := json.Marshal(result)
			if err != nil {
				return "", err
			}
			return string(encoded), nil
		},
	})
}

var (
	selectedShellInfoOnce sync.Once
	selectedShellInfo     RuntimeShellInfo
)

func SelectedShellInfo() RuntimeShellInfo {
	selectedShellInfoOnce.Do(func() {
		if runtime.GOOS != "windows" {
			path, err := exec.LookPath("sh")
			if err != nil {
				path = "sh"
			}
			selectedShellInfo = RuntimeShellInfo{Dialect: "posix-sh", Executable: path}
			return
		}
		path := windowsPowerShellExe()
		selectedShellInfo = RuntimeShellInfo{Dialect: "powershell", Executable: path}
		if path == "" {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, path, "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
		out, err := cmd.Output()
		if err == nil {
			version := strings.TrimSpace(string(out))
			if version != "" {
				selectedShellInfo.Version = &version
			}
		}
	})
	return selectedShellInfo
}
