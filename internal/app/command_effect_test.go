package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// Use production registration/validation but replace execution with a recorder.
// Deliberately misleading read_only declarations must never execute real commands.
func declaredCommandFixture(t *testing.T, name string, mode ReviewMode, interactive bool) (*Mods, proto.ToolResultCaller, *atomic.Int32, map[string]any) {
	t.Helper()
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.ReviewMode, cfg.InteractiveTTYAvailable = mode, interactive
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	m.reviewer.startSession()
	t.Cleanup(m.reviewer.reset)
	registered := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(registered, toolregistry.ShellConfig{Root: cfg.WorkingDir}))
	require.NoError(t, toolregistry.RegisterPowerShell(registered, toolregistry.ShellConfig{Root: cfg.WorkingDir}))
	require.NoError(t, toolregistry.RegisterProcess(registered, toolregistry.ProcessConfig{Root: cfg.WorkingDir}))
	tool, ok := registered.Tool(name)
	require.True(t, ok)
	called := &atomic.Int32{}
	tool.Call = func(ctx context.Context, data json.RawMessage) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		called.Add(1)
		return string(data), nil
	}
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(tool))
	exe, err := os.Executable()
	require.NoError(t, err)
	args := map[string]any{
		"purpose": "Delete the important directory",
		"command": "Remove-Item -Recurse ./important; rm -rf ./important",
		"program": exe, "args": []string{"--delete-everything"},
	}
	return m, m.toolResultCaller(registry, &cfg), called, args
}

func TestCommandDeclarationControlsDispatch(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
			for _, effect := range []string{"read_only", "may_write"} {
				t.Run(name+"/"+string(mode)+"/"+effect, func(t *testing.T) {
					m, caller, called, args := declaredCommandFixture(t, name, mode, false)
					// The cwd is temporary and both local and remote saved grants exist.
					// Neither may turn a may_write command into an automatic approval.
					m.reviewer.rules.Add(RulesForDirs([]string{m.Config.WorkingDir}, m.reviewer.scope, AccessWrite)...)
					m.reviewer.rules.Add(RulesForRemoteOrigins([]string{"https://example.com"})...)
					args["effect"] = effect
					data, err := json.Marshal(args)
					require.NoError(t, err)
					result, err := caller(proto.ToolCallRequest{Name: name, Arguments: data})
					if effect == "read_only" || mode == ReviewNever {
						require.NoError(t, err)
						require.Equal(t, int32(1), called.Load())
						require.JSONEq(t, string(data), result.Text)
					} else {
						require.ErrorIs(t, err, errReviewUnavailable)
						require.Zero(t, called.Load())
					}
					require.Empty(t, m.reviewer.reviewChan)
				})
			}
		}
	}
}

func TestCommandInvalidDeclarationNeverDispatches(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		for _, effect := range []any{nil, "", "read", "READ_ONLY", true, 1, []string{"read_only"}} {
			t.Run(name+"/"+stringMustJSON(t, effect), func(t *testing.T) {
				m, caller, called, args := declaredCommandFixture(t, name, ReviewNever, true)
				if effect != nil {
					args["effect"] = effect
				}
				_, err := caller(proto.ToolCallRequest{Name: name, Arguments: []byte(stringMustJSON(t, args))})
				require.ErrorContains(t, err, "effect")
				require.Zero(t, called.Load())
				require.Empty(t, m.reviewer.reviewChan)
			})
		}
	}
}

func TestCommandMissingPurposeNeverDispatches(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
			m, caller, called, args := declaredCommandFixture(t, name, mode, true)
			args["effect"] = "may_write"
			delete(args, "purpose")
			_, err := caller(proto.ToolCallRequest{Name: name, Arguments: []byte(stringMustJSON(t, args))})
			require.ErrorContains(t, err, "purpose")
			require.Zero(t, called.Load())
			require.Empty(t, m.reviewer.reviewChan)
		}
	}
}

func TestCommandReviewRedactsPurposeAndCommandBeforeFormatting(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		t.Run(name, func(t *testing.T) {
			m, caller, called, args := declaredCommandFixture(t, name, ReviewAuto, true)
			// The secret words must not occur in the fixture's temporary path:
			// macOS canonicalizes temp dirs under /private, so a word such as
			// "private" would trip the absence check for an unrelated reason.
			const secretBody = "hunter2\nswordfish"
			m.secrets = secrets.New()
			m.secrets.RememberForRedaction(secretBody)
			args["effect"] = "may_write"
			args["purpose"] = "Send \x1b[2J" + secretBody + " to https://example.com/?token=hidden"
			args["command"] = "echo " + secretBody
			args["args"] = []string{secretBody}
			done := make(chan error, 1)
			go func() {
				_, err := caller(proto.ToolCallRequest{Name: name, Arguments: []byte(stringMustJSON(t, args))})
				done <- err
			}()
			item := receiveReviewItem(t, m.reviewer.reviewChan)
			m.reviewer.handleStartMsg(toolReviewStartMsg{item: item})
			view := m.reviewer.renderBanner(160, makeStyles(true).Interaction, 30)
			for _, secret := range []string{"hunter2", "swordfish", "token=hidden", "\x1b[2J"} {
				require.NotContains(t, view, secret)
			}
			require.Contains(t, view, "[REDACTED]")
			require.Contains(t, view, `\u001b[2J`)
			m.reviewer.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
			require.ErrorContains(t, <-done, "denied")
			require.Zero(t, called.Load())
		})
	}
}

func stringMustJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}

func TestCommandReviewApprovesOnlyExactCall(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		for _, action := range []string{"approve", "deny", "cancel"} {
			t.Run(name+"/"+action, func(t *testing.T) {
				m, caller, called, args := declaredCommandFixture(t, name, ReviewAuto, true)
				ctx, cancel := context.WithCancel(m.ctx)
				defer cancel()
				m.ctx = ctx
				args["effect"] = "may_write"
				args["command"] = strings.Repeat("echo many lines\n", 60)
				args["args"] = []string{"script path.py", "literal\nargument", ""}
				data := []byte(stringMustJSON(t, args))
				done := make(chan error, 1)
				go func() {
					result, err := caller(proto.ToolCallRequest{Name: name, Arguments: data})
					if err == nil && !jsonEqual(data, []byte(result.Text)) {
						done <- os.ErrInvalid
						return
					}
					done <- err
				}()
				item := receiveReviewItem(t, m.reviewer.reviewChan)
				require.Zero(t, called.Load())
				require.Empty(t, item.candidateRules)
				require.JSONEq(t, string(data), string(item.args))
				require.Contains(t, item.presentation.rows, interactionRow{Label: "Purpose", Value: args["purpose"].(string)})
				m.reviewer.handleStartMsg(toolReviewStartMsg{item: item})
				banner := m.reviewer.renderBanner(60, makeStyles(true).Interaction, 12)
				require.NotContains(t, banner, "Review all pages")
				require.NotContains(t, banner, "Simplify")
				require.NotContains(t, banner, "Allow for session")
				for _, hidden := range []string{"Model declaration", "may_write", "Interpreter", "Warning", "Source:", "Argv:"} {
					require.NotContains(t, banner, hidden)
				}
				switch action {
				case "approve":
					m.reviewer.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
					require.NoError(t, <-done)
					require.Equal(t, int32(1), called.Load())
					// Approval is not remembered for the next identical call.
					m.reviewer.interactiveReviewAvailable = false
					_, err := caller(proto.ToolCallRequest{Name: name, Arguments: data})
					require.ErrorIs(t, err, errReviewUnavailable)
					require.Equal(t, int32(1), called.Load())
				case "deny":
					m.reviewer.handleKey(tea.KeyPressMsg{Code: 'n', Text: "n"})
					require.ErrorContains(t, <-done, "denied")
					require.Zero(t, called.Load())
				case "cancel":
					cancel()
					require.ErrorIs(t, <-done, context.Canceled)
					require.Zero(t, called.Load())
				}
			})
		}
	}
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xx, _ := json.Marshal(x)
	yy, _ := json.Marshal(y)
	return string(xx) == string(yy)
}

func TestDeclaredCommandNormalizesWorkingDirectory(t *testing.T) {
	m, caller, called, args := declaredCommandFixture(t, "shell_run", ReviewAuto, false)
	require.NoError(t, os.Mkdir(filepath.Join(m.Config.WorkingDir, "child"), 0700))
	args["effect"], args["cwd"] = "read_only", "child"
	result, err := caller(proto.ToolCallRequest{Name: "shell_run", Arguments: []byte(stringMustJSON(t, args))})
	require.NoError(t, err)
	var actual map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Text), &actual))
	require.Equal(t, filepath.Join(m.Config.ResolveWorkingDir().Canonical, "child"), actual["cwd"])
	require.Equal(t, int32(1), called.Load())
}

func TestDeclaredReadLoopExecutesWithoutReview(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir, cfg.ReviewMode, cfg.InteractiveTTYAvailable = t.TempDir(), ReviewAuto, false
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(registry, toolregistry.ShellConfig{Root: cfg.WorkingDir}))
	command := `i=1; sum=0; while [ "$i" -le 100 ]; do sum=$((sum+i)); i=$((i+1)); done; printf '%s\n' "$sum"`
	if runtime.GOOS == "windows" {
		command = `$sum = 0; foreach ($n in 1..100) { $sum += $n }; Write-Output $sum`
	}
	data := []byte(stringMustJSON(t, map[string]string{"command": command, "effect": "read_only", "purpose": "Sum integers 1 through 100 in memory"}))
	result, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
	require.NoError(t, err)
	require.Equal(t, "5050", strings.TrimSpace(result.Text))
}

func TestNativeToolIgnoresModelCommandDeclaration(t *testing.T) {
	cfg := defaultConfig()
	cfg.WorkingDir, cfg.ReviewMode, cfg.InteractiveTTYAvailable = t.TempDir(), ReviewAuto, false
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	for _, name := range []string{"fs_write_file", "mcp_update"} {
		registry := toolregistry.NewRegistry()
		called := false
		require.NoError(t, registry.Register(toolregistry.Tool{
			Spec: proto.ToolSpec{Name: name}, Capabilities: toolregistry.ToolCapabilities{Mutable: true},
			Call: func(context.Context, json.RawMessage) (string, error) { called = true; return "", nil },
		}))
		_, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{Name: name, Arguments: []byte(`{"effect":"read_only","path":"file","content":"x"}`)})
		require.ErrorIs(t, err, errReviewUnavailable)
		require.False(t, called)
	}
}

func TestDeclaredReadElevationReachesExecutionLayer(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		_, caller, called, args := declaredCommandFixture(t, name, ReviewAuto, false)
		args["effect"], args["elevated"] = "read_only", true
		// Mock dispatch only: the actual execution layer still owns UAC consent.
		_, err := caller(proto.ToolCallRequest{Name: name, Arguments: []byte(stringMustJSON(t, args))})
		require.NoError(t, err)
		require.Equal(t, int32(1), called.Load())
	}
}
