package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/watch"
)

type watchOutputKey struct{}

// Start only runs after process_run's ordinary declaration and review gate.
// Preserve prepared executable/path bindings but use the invocation lifetime.
func startWatchedProcess(ctx context.Context, cfg ProcessConfig, root string, args processRunArgs) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	manager := watch.FromContext(ctx)
	if manager == nil || !cfg.AllowWatch {
		return "", fmt.Errorf("watch requires --watch process")
	}
	if args.Elevated || len(args.SecretEnv) > 0 {
		return "", fmt.Errorf("watched processes do not support elevation or secret_env")
	}
	cfg.Progress = nil
	// Background jobs cannot open credential UI after their tool call returns.
	cfg.SudoPrompt = nil
	cfg.GitAuth = nil
	args.Watch = false
	values := context.WithoutCancel(ctx)
	err := manager.Start(FormatProcessInvocation(args.Program, args.Args), func(lifetime context.Context, w io.Writer) string {
		runCtx, cancel := context.WithCancel(values)
		defer cancel()
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
		if lifetime.Err() != nil {
			cancel()
		}
		runCtx = context.WithValue(runCtx, watchOutputKey{}, w)
		result, err := runProcess(runCtx, cfg, root, args)
		if err != nil {
			return fmt.Sprintf("process failed: %v", err)
		}
		var completed struct {
			ExitCode   *int  `json:"exit_code"`
			DurationMS int64 `json:"duration_ms"`
			TimedOut   bool  `json:"timed_out"`
		}
		if err := json.Unmarshal([]byte(result), &completed); err != nil {
			return fmt.Sprintf("invalid process completion: %v", err)
		}
		encoded, _ := json.Marshal(completed)
		return string(encoded)
	})
	if err != nil {
		return "", err
	}
	return "Process accepted for monitoring. Output and exit status will arrive in subsequent watch events. This does not confirm startup or success.", nil
}

func RegisterWatch(registry *Registry) error {
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "watch_control",
			Description: "Inspect or stop this invocation's watch task. Stop ends monitoring and terminates its owned process tree; it never terminates a process writing an observed file. Use stop when the user's completion or retry limit is reached. No persisted permissions or automatic resume.",
			InputSchema: objectSchema(map[string]any{"action": map[string]any{"type": "string", "enum": []string{"status", "stop"}}}, "action"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Action string `json:"action"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			m := watch.FromContext(ctx)
			if m == nil {
				return "", fmt.Errorf("no watch invocation")
			}
			switch args.Action {
			case "status":
			case "stop":
				m.Stop()
			default:
				return "", fmt.Errorf("action must be status or stop")
			}
			b, err := json.Marshal(m.Status())
			return string(b), err
		},
	})
}
