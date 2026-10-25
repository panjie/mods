package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// These fixtures exercise review and the execution-time reassessment together.
// A grant for allowed must never authorize an operand resolving into external.
func TestCommandPathNormalizationCannotBorrowDirectoryGrant(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell and symlink fixtures")
	}
	for _, kind := range []string{"trailing space", "parent traversal", "ordinary space"} {
		for _, form := range []string{"shell", "process", "redirect", "copy"} {
			t.Run(kind+"/"+form, func(t *testing.T) {
				root, err := filepath.EvalSymlinks(t.TempDir())
				require.NoError(t, err)
				allowed, external := filepath.Join(root, "allowed"), filepath.Join(root, "external")
				require.NoError(t, os.MkdirAll(allowed, 0700))
				require.NoError(t, os.MkdirAll(filepath.Join(external, "child"), 0700))
				victim := filepath.Join(external, "victim")
				operand := filepath.Join(allowed, "ordinary space")
				switch kind {
				case "trailing space":
					operand = filepath.Join(allowed, "link ")
					require.NoError(t, os.Symlink(victim, operand))
				case "parent traversal":
					require.NoError(t, os.Symlink(filepath.Join(external, "child"), filepath.Join(allowed, "link")))
					operand = allowed + "/link/../victim"
				}
				src := filepath.Join(root, "source")
				require.NoError(t, os.WriteFile(src, []byte("fixture"), 0600))
				command := "touch '" + operand + "'"
				name := "shell_run"
				var args json.RawMessage
				switch form {
				case "process":
					name = "process_run"
					args, _ = json.Marshal(map[string]any{"program": "touch", "args": []string{operand}})
				case "redirect":
					command = "printf fixture > '" + operand + "'"
				case "copy":
					command = "cp '" + src + "' '" + operand + "'"
				}
				if args == nil {
					args, _ = json.Marshal(map[string]string{"command": command})
				}
				cfg := defaultConfig()
				cfg.WorkingDir = root
				cfg.ReviewMode = ReviewAuto
				cfg.InteractiveTTYAvailable = false
				m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
				m.reviewer.rules.Add(RulesForDirs([]string{allowed}, WorkingDirScope(root), AccessWrite)...)
				calls := 0
				registry := toolregistry.NewRegistry()
				require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: name}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(ctx context.Context, _ json.RawMessage) (string, error) {
					calls++
					var cmd *exec.Cmd
					if form == "process" {
						cmd = exec.CommandContext(ctx, "touch", operand)
					} else {
						cmd = exec.CommandContext(ctx, "sh", "-c", command)
					}
					out, err := cmd.CombinedOutput()
					return string(out), err
				}}))
				_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: name, Arguments: args})
				if kind == "ordinary space" {
					require.NoError(t, err)
					require.Equal(t, 1, calls)
					_, err = os.Stat(operand)
					require.NoError(t, err)
				} else {
					require.ErrorIs(t, err, errReviewUnavailable)
					require.Zero(t, calls)
				}
				_, err = os.Stat(victim)
				require.True(t, os.IsNotExist(err), "external file must remain absent")
			})
		}
	}
}
