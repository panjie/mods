package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
)

// Script arguments carry the immutable source itself, never a mutable file
// path. The selected interpreter is pinned before review by the app caller.
type scriptArgs struct {
	Interpreter string            `json:"interpreter"`
	Source      string            `json:"source"`
	Cwd         string            `json:"cwd"`
	Args        []string          `json:"args"`
	SecretEnv   map[string]string `json:"secret_env"`
	TimeoutMS   *int64            `json:"timeout_ms"`
}

func scriptInvocation(data json.RawMessage) (processRunArgs, error) {
	var script scriptArgs
	if err := json.Unmarshal(data, &script); err != nil {
		return processRunArgs{}, fmt.Errorf("invalid script arguments")
	}
	if strings.TrimSpace(script.Source) == "" || len(script.Source) > 8192 || strings.ContainsRune(script.Source, 0) {
		return processRunArgs{}, fmt.Errorf("source must contain 1-8192 bytes of complete readable script; no NUL bytes")
	}
	if len(script.Args) > 32 {
		return processRunArgs{}, fmt.Errorf("script accepts at most 32 arguments")
	}
	if err := validateSecretEnv(script.SecretEnv); err != nil {
		return processRunArgs{}, err
	}
	p := processRunArgs{Cwd: script.Cwd, SecretEnv: script.SecretEnv, TimeoutMS: script.TimeoutMS}
	switch script.Interpreter {
	case "sh":
		if runtime.GOOS == "windows" {
			return p, fmt.Errorf("use powershell on Windows")
		}
		p.Program = "sh"
		p.Args = []string{"-c", script.Source, "mods-script"}
	case "powershell":
		if len(script.Args) > 0 {
			return p, fmt.Errorf("PowerShell script args are unsupported; put literal parameters in source")
		}
		p.Program = "pwsh"
		if runtime.GOOS == "windows" {
			p.Program = windowsPowerShellExe()
		}
		p.Args = []string{"-NoProfile", "-NonInteractive", "-Command", script.Source}
	case "python":
		p.Program = "python3"
		if runtime.GOOS == "windows" {
			p.Program = "python"
		}
		p.Args = []string{"-c", script.Source}
	case "node":
		p.Program = "node"
		p.Args = []string{"-e", script.Source, "--"}
	case "emacs":
		if len(script.Args) > 0 {
			return p, fmt.Errorf("Emacs script args are unsupported")
		}
		p.Program = "emacs"
		p.Args = []string{"--batch", "--eval", script.Source}
	default:
		return p, fmt.Errorf("interpreter must be sh, powershell, python, node or emacs")
	}
	argumentBytes := 0
	for _, arg := range script.Args {
		argumentBytes += len(arg)
		if strings.ContainsRune(arg, 0) || len(arg) > 1024 {
			return p, fmt.Errorf("script argument contains NUL or exceeds 1024 bytes")
		}
	}
	if argumentBytes > 4096 {
		return p, fmt.Errorf("script arguments exceed 4096 bytes in total")
	}
	p.Args = append(p.Args, script.Args...)
	return p, nil
}

// ScriptProcessArguments is used for executable pinning, not effect inference.
func ScriptProcessArguments(data json.RawMessage) ([]byte, error) {
	args, err := scriptInvocation(data)
	if err != nil {
		return nil, err
	}
	return json.Marshal(args)
}

func RegisterScript(registry *Registry, cfg ProcessConfig) error {
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultShellTimeout
	}
	return registry.Register(Tool{
		Kind:          ToolKindBuiltin,
		TimeoutPolicy: TimeoutPolicySelf,
		Capabilities:  ToolCapabilities{Mutable: true},
		Validate:      func(data json.RawMessage) error { _, err := scriptInvocation(data); return err },
		IntentExtractor: func(json.RawMessage) approval.AccessIntent {
			return approval.AccessIntent{Class: approval.AccessWrite, UncertainEffect: true}
		},
		Spec: proto.ToolSpec{
			Name:        "script_run",
			Description: "Execute a necessary script after full source review. Prefer structured file/download tools and simple process calls for routine work. Supply the complete readable source (up to 8192 bytes), never an encoded payload or a wrapper hiding another script. Review covers this source snapshot, interpreter, cwd and literal args; imports and child processes can have unknown effects. Requires interactive one-time approval except in review-mode never. Uses secret_env for credentials.",
			InputSchema: objectSchema(map[string]any{
				"interpreter": map[string]any{"type": "string", "enum": []string{"sh", "powershell", "python", "node", "emacs"}},
				"source":      stringProp("Complete readable script source, not a file path."),
				"cwd":         stringProp("Literal working directory; defaults to workspace."),
				"args":        map[string]any{"type": "array", "items": stringProp("Literal argument; sh/python/node only.")},
				"secret_env":  map[string]any{"type": "object", "additionalProperties": map[string]any{"type": "string"}, "description": "Environment names mapped to secure secret references."},
				"timeout_ms":  integerProp("Optional positive execution timeout."),
			}, "interpreter", "source"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			args, err := scriptInvocation(data)
			if err != nil {
				return "", err
			}
			return runProcess(ctx, cfg, cfg.Root, args)
		},
	})
}
