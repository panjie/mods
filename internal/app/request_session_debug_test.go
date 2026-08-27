package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestToolDebugLifecycleIncludesCallMetadataAndRedactsSecrets(t *testing.T) {
	var output bytes.Buffer
	restore := debugpkg.SetOutputForTest(&output)
	debugpkg.SetEnabled(true)
	t.Cleanup(func() {
		debugpkg.SetEnabled(false)
		restore()
	})

	cfg := defaultConfig()
	cfg.ReviewMode = ReviewNever
	cfg.MCPTimeout = time.Second
	store := secrets.New()
	_, err := store.Put("super-secret-value", secrets.Target{Tool: "lookup", Path: "/token"})
	require.NoError(t, err)
	m := &Mods{
		Config:     &cfg,
		ctx:        context.Background(),
		reviewer:   newToolReviewer(&cfg),
		secrets:    store,
		debugTurn:  3,
		debugRound: 2,
	}
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{
			Name: "lookup",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"token"},
				"properties": map[string]any{"token": map[string]any{"type": "string"}},
			},
		},
		Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			return "received super-secret-value", nil
		},
	}))

	result, err := m.toolCaller(registry, &cfg, "")(proto.ToolCallRequest{
		ID: "call_xyz", Index: 2, Total: 3, Name: "lookup",
		Arguments: []byte(`{"token":"super-secret-value"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "received [REDACTED]", result)

	log := output.String()
	require.Contains(t, log, "tool · turn 3 · round 2 · call 2/3 · start")
	require.Contains(t, log, "lookup [call_xyz]")
	require.Contains(t, log, "approval")
	require.Contains(t, log, "auto")
	require.Contains(t, log, "success")
	require.Contains(t, log, "received [REDACTED]")
	require.NotContains(t, log, "super-secret-value")
}

func TestToolDebugStatusDistinguishesOutcomes(t *testing.T) {
	status, _ := toolDebugStatus(nil)
	require.Equal(t, "success", status)
	status, _ = toolDebugStatus(toolregistry.ShellExitError{Code: 7})
	require.Equal(t, "exit 7", status)
	status, _ = toolDebugStatus(context.Canceled)
	require.Equal(t, "cancelled", status)
	status, _ = toolDebugStatus(errReviewUnavailable)
	require.Equal(t, "denied", status)
}

func TestToolDebugLifecycleRecordsValidationFailure(t *testing.T) {
	var output bytes.Buffer
	restore := debugpkg.SetOutputForTest(&output)
	debugpkg.SetEnabled(true)
	t.Cleanup(func() {
		debugpkg.SetEnabled(false)
		restore()
	})

	cfg := defaultConfig()
	cfg.MCPTimeout = time.Second
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg), secrets: secrets.New(), debugTurn: 1, debugRound: 1}
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "required_arg", InputSchema: map[string]any{
			"type": "object", "required": []string{"path"},
			"properties": map[string]any{"path": map[string]any{"type": "string"}},
		}},
		Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		Call:         func(context.Context, json.RawMessage) (string, error) { return "unexpected", nil },
	}))

	_, err := m.toolCaller(registry, &cfg, "")(proto.ToolCallRequest{ID: "bad_call", Index: 1, Total: 1, Name: "required_arg", Arguments: []byte(`{}`)})
	require.Error(t, err)
	require.Contains(t, output.String(), "required_arg [bad_call]")
	require.Contains(t, output.String(), "failed")
	require.Contains(t, output.String(), "not reached")
}
