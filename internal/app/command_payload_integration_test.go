package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestScriptPayloadChangeSharesSingleReapproval(t *testing.T) {
	for _, tc := range []struct {
		name          string
		changeAgain   bool
		wantExecution int32
	}{
		{name: "one change requires fresh review", wantExecution: 1},
		{name: "continuous change stops dispatch", changeAgain: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "script.py")
			require.NoError(t, os.WriteFile(script, []byte("print('first')\n"), 0o600))
			program := filepath.Join(root, "python")
			data, err := json.Marshal(map[string]any{"program": program, "args": []string{script}, "cwd": root})
			require.NoError(t, err)
			cfg := defaultConfig()
			cfg.WorkingDir, cfg.ReviewMode = root, ReviewAuto
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(root), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
			m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
			registry := toolregistry.NewRegistry()
			var executed atomic.Int32
			var received json.RawMessage
			require.NoError(t, registry.Register(toolregistry.Tool{
				Spec: proto.ToolSpec{Name: "process_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
				Call: func(_ context.Context, got json.RawMessage) (string, error) {
					executed.Add(1)
					received = append([]byte(nil), got...)
					return "ok", nil
				},
			}))
			done := make(chan error, 1)
			go func() {
				_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "process_run", Arguments: data})
				done <- err
			}()
			item := receiveReviewItem(t, r.reviewChan)
			require.Contains(t, item.presentation.rows, interactionRow{Label: "Script source", Value: "1 | print('first')\n2 | "})
			require.NoError(t, os.WriteFile(script, []byte("print('second')\n"), 0o600))
			item.resp <- reviewResponse{action: reviewActionApprove}
			item = receiveReviewItem(t, r.reviewChan)
			require.Contains(t, item.presentation.rows, interactionRow{Label: "Script source", Value: "1 | print('second')\n2 | "})
			require.Zero(t, executed.Load())
			if tc.changeAgain {
				require.NoError(t, os.WriteFile(script, []byte("print('third')\n"), 0o600))
			}
			item.resp <- reviewResponse{action: reviewActionApprove}
			err = <-done
			if tc.changeAgain {
				require.ErrorContains(t, err, "script payload changed repeatedly")
			} else {
				require.NoError(t, err)
				require.JSONEq(t, string(data), string(received))
			}
			require.Equal(t, tc.wantExecution, executed.Load())
		})
	}
}

func TestScriptPayloadDoesNotBlockReviewNever(t *testing.T) {
	root := t.TempDir()
	program := filepath.Join(root, "python")
	data, err := json.Marshal(map[string]any{"program": program, "args": []string{filepath.Join(root, "missing.py")}})
	require.NoError(t, err)
	cfg := defaultConfig()
	cfg.WorkingDir, cfg.ReviewMode = root, ReviewNever
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	called := false
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "process_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { called = true; return "ok", nil },
	}))
	_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "process_run", Arguments: data})
	require.NoError(t, err)
	require.True(t, called)
}

func TestCommandPayloadPreviewRedactsKnownSecrets(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "script.py")
	require.NoError(t, os.WriteFile(script, []byte("print('secret-token')\n"), 0o600))
	payload := preparePayloadFixture(t, filepath.Join(root, "python"), []string{script}, root)
	rows := payload.reviewRows(func(value string) string { return strings.ReplaceAll(value, "secret-token", "[REDACTED]") })
	require.Contains(t, rows, interactionRow{Label: "Script source", Value: "1 | print('[REDACTED]')\n2 | "})
}
