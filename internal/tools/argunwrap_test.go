package tools

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestUnwrapToolArguments(t *testing.T) {
	plainSchema := objectSchema(map[string]any{
		"question": stringProp("question"),
		"kind":     stringProp("kind"),
	}, "question", "kind")
	owningSchema := objectSchema(map[string]any{
		"arguments": stringProp("legitimate top-level property"),
	}, "arguments")

	tests := []struct {
		name   string
		schema map[string]any
		data   string
		want   string
	}{
		{"arguments object wrapper", plainSchema, `{"arguments":{"question":"hi","kind":"text"}}`, `{"question":"hi","kind":"text"}`},
		{"arguments string wrapper", plainSchema, `{"arguments":"{\"question\":\"hi\",\"kind\":\"text\"}"}`, `{"question":"hi","kind":"text"}`},
		{"parameters object wrapper", plainSchema, `{"parameters":{"question":"hi"}}`, `{"question":"hi"}`},
		{"parameters string wrapper", plainSchema, `{"parameters":"{\"question\":\"hi\"}"}`, `{"question":"hi"}`},
		{"schema owns wrapper key", owningSchema, `{"arguments":{"question":"hi"}}`, `{"arguments":{"question":"hi"}}`},
		{"multiple keys untouched", plainSchema, `{"name":"request_user_input","arguments":{"question":"hi"}}`, `{"name":"request_user_input","arguments":{"question":"hi"}}`},
		{"other single key untouched", plainSchema, `{"question":"hi"}`, `{"question":"hi"}`},
		{"non-object wrapper value untouched", plainSchema, `{"arguments":"not json"}`, `{"arguments":"not json"}`},
		{"scalar array wrapper value untouched", plainSchema, `{"arguments":["a","b"]}`, `{"arguments":["a","b"]}`},
		{"non-object payload untouched", plainSchema, `["a","b"]`, `["a","b"]`},
		{"nil schema unwraps", nil, `{"arguments":{"question":"hi"}}`, `{"question":"hi"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := UnwrapToolArguments(tc.schema, []byte(tc.data))
			require.JSONEq(t, tc.want, string(got))
		})
	}

	t.Run("empty payload returned unchanged", func(t *testing.T) {
		require.Nil(t, UnwrapToolArguments(plainSchema, nil))
	})

	t.Run("malformed payload returned unchanged", func(t *testing.T) {
		require.Equal(t, `{`, string(UnwrapToolArguments(plainSchema, []byte(`{`))))
	})

	t.Run("direct object wrapper preserves raw bytes", func(t *testing.T) {
		data := []byte(`{"arguments":{"question":"你的16寸MacBook内存多大","kind":"select"}}`)
		got := UnwrapToolArguments(plainSchema, data)
		require.Equal(t, `{"question":"你的16寸MacBook内存多大","kind":"select"}`, string(got))
	})
}

func TestRegistryUnwrapArguments(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, registry.Register(Tool{
		Spec: proto.ToolSpec{
			Name: "ask",
			InputSchema: objectSchema(map[string]any{
				"question": stringProp("question"),
			}, "question"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			return string(data), nil
		},
	}))
	require.NoError(t, registry.Register(Tool{
		Spec: proto.ToolSpec{
			Name: "envelope",
			InputSchema: objectSchema(map[string]any{
				"arguments": stringProp("legitimate"),
			}, "arguments"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			return string(data), nil
		},
	}))

	require.JSONEq(t, `{"question":"hi"}`, string(registry.UnwrapArguments("ask", []byte(`{"arguments":{"question":"hi"}}`))))
	require.JSONEq(t, `{"arguments":"x"}`, string(registry.UnwrapArguments("envelope", []byte(`{"arguments":"x"}`))))
	require.Equal(t, `{"question":"hi"}`, string(registry.UnwrapArguments("missing", []byte(`{"question":"hi"}`))))
}
