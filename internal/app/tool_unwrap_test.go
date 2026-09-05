package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// A lone "arguments" wrapper around a tool call's real parameters must be
// unwrapped before validation and dispatch so weak-model calls succeed
// without a wasted retry round. The double-encoded string form observed with
// small models is covered as well.
func TestToolCallerUnwrapsArgumentsWrapper(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewNever
	m := &Mods{
		Config:   &cfg,
		ctx:      context.Background(),
		reviewer: newToolReviewer(&cfg),
	}
	registry := toolregistry.NewRegistry()
	var received []byte
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{
			Name: "lookup",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"query"},
				"properties": map[string]any{"query": map[string]any{"type": "string"}},
			},
		},
		Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			received = append([]byte(nil), data...)
			return "ok", nil
		},
	}))

	for name, arguments := range map[string]string{
		"object wrapper":     `{"arguments":{"query":"mods"}}`,
		"string wrapper":     `{"arguments":"{\"query\":\"mods\"}"}`,
		"parameters wrapper": `{"parameters":{"query":"mods"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			out, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{
				ID: "call_wrapped", Index: 1, Total: 1, Name: "lookup", Arguments: []byte(arguments),
			})
			require.NoError(t, err)
			require.Equal(t, "ok", out)
			require.JSONEq(t, `{"query":"mods"}`, string(received))
		})
	}
}
