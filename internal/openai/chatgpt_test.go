package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestChatGPTSubscriptionToolContinuation(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		requests = append(requests, body)
		require.Equal(t, false, body["store"])
		require.Equal(t, true, body["stream"])
		for _, field := range []string{"user", "temperature", "max_output_tokens", "previous_response_id", "metadata", "conversation", "background", "top_p"} {
			require.NotContains(t, body, field)
		}
		input := body["input"].([]any)
		require.Equal(t, "developer", input[0].(map[string]any)["role"])
		tools := body["tools"].([]any)
		ns := tools[0].(map[string]any)
		require.Equal(t, "namespace", ns["type"])
		require.Equal(t, "mods", ns["name"])
		require.Equal(t, "lookup", ns["tools"].([]any)[0].(map[string]any)["name"])
		if len(requests) == 1 {
			writeResponsesSSE(t, w, completedResponseEvent(`[{"type":"function_call","id":"fc1","call_id":"call1","namespace":"mods","name":"lookup","arguments":"{}","status":"completed"}]`, 2, 3))
		} else {
			require.Len(t, input, 4)
			require.Equal(t, "mods", input[2].(map[string]any)["namespace"])
			require.Equal(t, "function_call_output", input[3].(map[string]any)["type"])
			writeResponsesSSE(t, w, completedResponseEvent(`[{"type":"message","id":"m1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]`, 3, 4))
		}
	}))
	defer server.Close()
	temperature := 0.5
	limit := int64(100)
	calls := 0
	s := New(Config{AuthToken: "test", BaseURL: server.URL, UseResponses: true, ProviderProfile: ProviderProfileChatGPT, ExtraParams: map[string]any{"temperature": 1, "metadata": map[string]any{"x": "y"}, "tools": []any{}, "store": true, "previous_response_id": "bad", "input": []any{}, "stream": false}}).Request(context.Background(), proto.Request{
		Model: "account-model", User: "user", Temperature: &temperature, MaxTokens: &limit,
		Messages: []proto.Message{{Role: proto.RoleSystem, Content: "instructions"}, {Role: proto.RoleUser, Content: "lookup"}},
		Tools:    []proto.ToolSpec{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}},
		ResultCaller: func(call proto.ToolCallRequest) (proto.ToolResult, error) {
			calls++
			require.Equal(t, "lookup", call.Name)
			return proto.ToolResult{Text: "result"}, nil
		},
	})
	defer s.Close()
	drainStream(t, s)
	require.Len(t, s.CallTools(), 1)
	drainStream(t, s)
	require.Equal(t, 1, calls)
	require.Len(t, requests, 2)
}

func TestChatGPTRebuildsToolHistoryWithNamespace(t *testing.T) {
	input, err := fromProtoResponseInput([]proto.Message{{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{ID: "call", Function: proto.Function{Name: "lookup", Arguments: json.RawMessage(`{}`)}}}}}, ProviderProfileChatGPT)
	require.NoError(t, err)
	b, err := json.Marshal(input)
	require.NoError(t, err)
	require.Contains(t, string(b), `"namespace":"mods"`)
}

func TestChatGPTUsageLimitIsTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeResponsesSSE(t, w, `{"type":"response.failed","response":{"id":"r","status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}}`)
	}))
	defer server.Close()
	s := New(Config{AuthToken: "test", BaseURL: server.URL, UseResponses: true, ProviderProfile: ProviderProfileChatGPT}).Request(context.Background(), proto.Request{Model: "model", Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}}})
	defer s.Close()
	for s.Next() {
		_, _ = s.Current()
	}
	require.ErrorContains(t, s.Err(), "ChatGPT plan usage limit reached")
	require.Empty(t, s.CallTools())
}
