package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// Exercise the real assessment -> approval -> dispatch path. A diagnostic must
// neither fabricate a failed result nor require a second invocation to execute.
func TestComplexCommandDispatchUsesOrdinaryApproval(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		mode      ReviewMode
		effect    commandanalysis.CommandEffect
		savedRule bool
		wantRun   bool
	}{
		{"classified read auto", ReviewAuto, commandanalysis.EffectRead, false, false},
		{"classified read always", ReviewAlways, commandanalysis.EffectRead, false, false},
		{"write requires approval", ReviewAuto, commandanalysis.EffectWrite, false, false},
		{"saved write target", ReviewAuto, commandanalysis.EffectWrite, true, false},
		{"always ignores saved target", ReviewAlways, commandanalysis.EffectWrite, true, false},
		{"unknown requires approval", ReviewAuto, commandanalysis.EffectUnknown, false, false},
		{"unknown is not covered by target rule", ReviewAuto, commandanalysis.EffectUnknown, true, false},
		{"explicit never", ReviewNever, commandanalysis.EffectUnknown, false, true},
	} {
		for _, minimal := range []bool{false, true} {
			t.Run(tc.name+"/minimal="+strconv.FormatBool(minimal), func(t *testing.T) {
				cfg := defaultConfig()
				cfg.WorkingDir, cfg.ReviewMode, cfg.Minimal = cwd, tc.mode, minimal
				cfg.InteractiveTTYAvailable = false
				m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}

				if tc.savedRule {
					m.reviewer.rules.Add(RulesForDirs([]string{cwd}, m.reviewer.scope, AccessWrite)...)
				}
				const command = `python -c "print(1)"`
				a := m.assessCommand("shell_run", command)
				require.True(t, a.HasReviewabilityConcerns(), "fixture must exercise complex-command diagnostics")
				require.False(t, a.StaticRead)
				data, err := json.Marshal(map[string]string{"command": command})
				require.NoError(t, err)
				registry := toolregistry.NewRegistry()
				var calls int
				require.NoError(t, registry.Register(toolregistry.Tool{
					Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
					Call: func(_ context.Context, got json.RawMessage) (string, error) {
						calls++
						require.JSONEq(t, string(data), string(got), "execute the original arguments unchanged")
						return "result", nil
					},
				}))
				message, status := stream.CallTool(proto.ToolCallRequest{Name: "shell_run", Arguments: data}, m.toolCaller(registry, &cfg))
				if tc.wantRun {
					require.NoError(t, status.Err)
					require.Equal(t, 1, calls)
					require.Equal(t, "result", message.Content)
					require.False(t, message.ToolCalls[0].IsError)
				} else {
					require.ErrorIs(t, status.Err, errReviewUnavailable)
					require.Zero(t, calls)
				}
			})
		}
	}
}

func TestComplexCommandInteractiveApprovalPreservesPayload(t *testing.T) {
	for _, allow := range []bool{false, true} {
		t.Run(strconv.FormatBool(allow), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.ReviewMode = ReviewAuto
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(cfg.WorkingDir), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
			m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
			registry := toolregistry.NewRegistry()
			var executed atomic.Int32
			var received json.RawMessage
			require.NoError(t, registry.Register(toolregistry.Tool{
				Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
				Call: func(_ context.Context, data json.RawMessage) (string, error) {
					received = append([]byte(nil), data...)
					executed.Add(1)
					return "ok", nil
				},
			}))
			// Merely storing a script under the temporary directory must not
			// authorize its unknown effects. No script is actually executed.
			data, err := json.Marshal(map[string]string{"command": `python "` + filepath.ToSlash(filepath.Join(cfg.WorkingDir, "count.py")) + `"`})
			require.NoError(t, err)
			finished := make(chan error, 1)
			go func() {
				_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
				finished <- err
			}()
			select {
			case item := <-r.reviewChan:
				require.Zero(t, executed.Load())
				require.JSONEq(t, string(data), string(item.args))
				item.resp <- reviewResponse{approved: allow}
			case err := <-finished:
				t.Fatalf("call returned before ordinary approval: %v", err)
			case <-ctx.Done():
				t.Fatal("call did not reach approval")
			}
			err = <-finished
			if allow {
				require.NoError(t, err)
				require.EqualValues(t, 1, executed.Load())
				require.JSONEq(t, string(data), string(received))
			} else {
				require.ErrorContains(t, err, "execution denied by user")
				require.Zero(t, executed.Load())
			}
		})
	}
}

func TestRelatedStaticReadsExecuteAsOneCall(t *testing.T) {
	command := `git ls-files; git diff --stat`
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.ReviewMode = ReviewAlways
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	if runtime.GOOS == "windows" {
		t.Cleanup(func() { commandanalysis.CloseBridge() })
	}
	a := m.assessCommand("shell_run", command)
	require.True(t, a.StaticRead)
	require.Greater(t, a.Shape.TopLevelActions, 1)
	registry := toolregistry.NewRegistry()
	var calls int
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { calls++; return "ok", nil },
	}))
	data, err := json.Marshal(map[string]string{"command": command})
	require.NoError(t, err)
	_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestComplexProcessDispatchDoesNotRequireResubmission(t *testing.T) {
	program, args := "sh", []string{"-c", "printf result"}
	if runtime.GOOS == "windows" {
		program, args = "powershell.exe", []string{"-Command", "Write-Output result"}
	}
	if _, err := exec.LookPath(program); err != nil {
		t.Skipf("fixture requires %s on PATH", program)
	}
	for _, effect := range []commandanalysis.CommandEffect{commandanalysis.EffectRead, commandanalysis.EffectUnknown} {
		t.Run(string(effect), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.ReviewMode = ReviewAuto
			cfg.InteractiveTTYAvailable = false
			m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
			data, err := json.Marshal(map[string]any{"program": program, "args": args})
			require.NoError(t, err)
			require.True(t, m.assessCommand("process_run", string(data)).HasReviewabilityConcerns())
			registry := toolregistry.NewRegistry()
			var calls int
			require.NoError(t, registry.Register(toolregistry.Tool{
				Spec: proto.ToolSpec{Name: "process_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
				Call: func(_ context.Context, got json.RawMessage) (string, error) {
					calls++
					require.JSONEq(t, string(data), string(got))
					return "result", nil
				},
			}))
			_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "process_run", Arguments: data})
			require.ErrorIs(t, err, errReviewUnavailable)
			require.Zero(t, calls)
		})
	}
}
