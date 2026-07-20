package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	SDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

func newTestClient(t *testing.T, cfg Config) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	cfg.BaseURL = server.URL
	return New(cfg)
}

func TestMessageBudgeterRunsInitiallyAndStopsFailedFollowup(t *testing.T) {
	wantErr := errors.New("budget exceeded")
	calls := 0
	client := newTestClient(t, DefaultConfig("test"))
	st := client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		MessageBudgeter: func(messages []proto.Message) ([]proto.Message, error) {
			calls++
			return nil, wantErr
		},
	})
	require.False(t, st.Next())
	require.ErrorIs(t, st.Err(), wantErr)
	require.Equal(t, 1, calls)

	followup := &Stream{
		done:     true,
		messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		budgeter: func(messages []proto.Message) ([]proto.Message, error) {
			calls++
			return nil, wantErr
		},
	}
	require.False(t, followup.Next())
	require.ErrorIs(t, followup.Err(), wantErr)
	require.Equal(t, 2, calls)
}

func TestNormalizeBaseURL(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"host root", "https://api.anthropic.com", "https://api.anthropic.com"},
		{"trailing v1", "https://api.anthropic.com/v1", "https://api.anthropic.com"},
		{"full messages endpoint", "https://api.anthropic.com/v1/messages", "https://api.anthropic.com"},
		{"documented gateway endpoint", "https://opencode.ai/zen/go/v1/messages", "https://opencode.ai/zen/go"},
		{"bare messages suffix", "https://gateway.example.com/messages", "https://gateway.example.com"},
		{"custom path preserved", "https://gateway.example.com/custom", "https://gateway.example.com/custom"},
		{"trailing v1 with custom path", "https://gateway.example.com/proxy/v1", "https://gateway.example.com/proxy"},
		{"trailing slash on v1", "https://host/v1/", "https://host"},
		{"trailing slash on messages", "https://host/v1/messages/", "https://host"},
		{"surrounding whitespace trimmed", "  https://host/v1/messages  ", "https://host"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := NormalizeBaseURL(c.in); got != c.want {
				t.Errorf("NormalizeBaseURL(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestTokenUsageFromMessageIncludesCacheTokens(t *testing.T) {
	message := SDK.Message{Usage: SDK.Usage{
		InputTokens: 7, CacheCreationInputTokens: 3,
		CacheReadInputTokens: 5, OutputTokens: 4,
	}}
	want := proto.TokenUsage{InputTokens: 15, OutputTokens: 4, TotalTokens: 19}
	if got := tokenUsageFromMessage(message); got != want {
		t.Fatalf("tokenUsageFromMessage() = %#v, want %#v", got, want)
	}
}

func TestThinkingRequestConfiguration(t *testing.T) {
	t.Run("adaptive maps effort and omits temperature", func(t *testing.T) {
		cfg := DefaultConfig("test")
		cfg.ThinkingType = "adaptive"
		cfg.ThinkingActive = true
		cfg.ReasoningEffort = "xhigh"
		client := newTestClient(t, cfg)
		temperature := 0.2

		st := client.Request(context.Background(), proto.Request{
			Model:       "claude-opus-4-8",
			Messages:    []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
			Temperature: &temperature,
		}).(*Stream)

		require.NotNil(t, st.request.Thinking.OfAdaptive)
		require.Equal(t, SDK.OutputConfigEffort("xhigh"), st.request.OutputConfig.Effort)
		require.False(t, st.request.Temperature.Valid())
	})

	t.Run("manual mode grows implicit max tokens", func(t *testing.T) {
		cfg := DefaultConfig("test")
		cfg.ThinkingType = "enabled"
		cfg.ThinkingActive = true
		cfg.ThinkingBudget = 8192
		client := newTestClient(t, cfg)

		st := client.Request(context.Background(), proto.Request{
			Model:    "claude-sonnet-4-5-20250929",
			Messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		}).(*Stream)

		require.Equal(t, int64(12288), st.request.MaxTokens)
		require.Equal(t, int64(8192), st.request.Thinking.OfEnabled.BudgetTokens)
	})

	t.Run("manual mode rejects explicit max at or below budget", func(t *testing.T) {
		cfg := DefaultConfig("test")
		cfg.ThinkingType = "enabled"
		cfg.ThinkingActive = true
		cfg.ThinkingBudget = 4096
		client := newTestClient(t, cfg)
		maxTokens := int64(4096)

		st := client.Request(context.Background(), proto.Request{
			Model:       "claude-sonnet-4-5-20250929",
			Messages:    []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
			MaxTokens:   &maxTokens,
			Temperature: nil,
		})

		require.False(t, st.Next())
		require.ErrorContains(t, st.Err(), "must be greater than thinking-budget")
	})

	t.Run("manual mode rejects too-small budget", func(t *testing.T) {
		cfg := DefaultConfig("test")
		cfg.ThinkingType = "enabled"
		cfg.ThinkingBudget = 512
		client := newTestClient(t, cfg)

		st := client.Request(context.Background(), proto.Request{
			Model:    "claude-sonnet-4-5-20250929",
			Messages: []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
		})

		require.False(t, st.Next())
		require.ErrorContains(t, st.Err(), "at least 1024")
	})

	t.Run("disabled is serialized without activating thinking", func(t *testing.T) {
		cfg := DefaultConfig("test")
		cfg.ThinkingType = "disabled"
		client := newTestClient(t, cfg)
		temperature := 0.4

		st := client.Request(context.Background(), proto.Request{
			Model:       "claude-sonnet-5",
			Messages:    []proto.Message{{Role: proto.RoleUser, Content: "hello"}},
			Temperature: &temperature,
		}).(*Stream)

		require.NotNil(t, st.request.Thinking.OfDisabled)
		require.True(t, st.request.Temperature.Valid())
	})
}

func TestCallToolsGroupsParallelResults(t *testing.T) {
	var message SDK.Message
	require.NoError(t, json.Unmarshal([]byte(`{
		"id":"msg_1",
		"type":"message",
		"role":"assistant",
		"model":"claude-sonnet-4-6",
		"stop_reason":"tool_use",
		"stop_sequence":null,
		"usage":{"input_tokens":1,"output_tokens":2},
		"content":[
			{"type":"tool_use","id":"tool_1","name":"first","input":{"n":1}},
			{"type":"tool_use","id":"tool_2","name":"second","input":{"n":2}}
		]
	}`), &message))
	st := &Stream{
		message: message,
		request: SDK.MessageNewParams{
			Messages: []SDK.MessageParam{SDK.NewUserMessage(SDK.NewTextBlock("start"))},
		},
		toolCall: func(name string, _ []byte) (string, error) {
			return name + " result", nil
		},
	}

	statuses := st.CallTools()

	require.Len(t, statuses, 2)
	require.Len(t, st.request.Messages, 2)
	results := st.request.Messages[1]
	require.Equal(t, SDK.MessageParamRoleUser, results.Role)
	require.Len(t, results.Content, 2)
	require.Equal(t, "tool_1", results.Content[0].OfToolResult.ToolUseID)
	require.Equal(t, "tool_2", results.Content[1].OfToolResult.ToolUseID)
}

func TestThinkingToolRoundReplaysOpaqueBlocksAfterBudgeting(t *testing.T) {
	var requests []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		var request map[string]any
		require.NoError(t, json.Unmarshal(body, &request))
		requests = append(requests, request)

		w.Header().Set("Content-Type", "text/event-stream")
		if len(requests) == 1 {
			_, _ = w.Write([]byte(
				"event: message_start\n" +
					"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":10,\"output_tokens\":0}}}\n\n" +
					"event: content_block_start\n" +
					"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\",\"signature\":\"\"}}\n\n" +
					"event: content_block_delta\n" +
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"summary\"}}\n\n" +
					"event: content_block_delta\n" +
					"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"opaque-signature\"}}\n\n" +
					"event: content_block_stop\n" +
					"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
					"event: content_block_start\n" +
					"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"encrypted\"}}\n\n" +
					"event: content_block_stop\n" +
					"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
					"event: content_block_start\n" +
					"data: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tool_1\",\"name\":\"lookup\",\"input\":{}}}\n\n" +
					"event: content_block_delta\n" +
					"data: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\\\"mods\\\"}\"}}\n\n" +
					"event: content_block_stop\n" +
					"data: {\"type\":\"content_block_stop\",\"index\":2}\n\n" +
					"event: message_delta\n" +
					"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":3}}\n\n" +
					"event: message_stop\n" +
					"data: {\"type\":\"message_stop\"}\n\n",
			))
			return
		}
		_, _ = w.Write([]byte(
			"event: message_start\n" +
				"data: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_2\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-sonnet-4-6\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":20,\"output_tokens\":0}}}\n\n" +
				"event: content_block_start\n" +
				"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\n" +
				"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"done\"}}\n\n" +
				"event: content_block_stop\n" +
				"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"event: message_delta\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":1}}\n\n" +
				"event: message_stop\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
		))
	}))
	defer server.Close()

	cfg := DefaultConfig("test")
	cfg.BaseURL = server.URL
	cfg.ThinkingType = "adaptive"
	cfg.ThinkingActive = true
	client := New(cfg)
	st := client.Request(context.Background(), proto.Request{
		Model:    "claude-sonnet-4-6",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "find it"}},
		Tools: []proto.ToolSpec{{
			Name:        "lookup",
			Description: "look something up",
			InputSchema: map[string]any{"type": "object"},
		}},
		ToolCaller: func(name string, data []byte) (string, error) {
			require.Equal(t, "lookup", name)
			require.JSONEq(t, `{"q":"mods"}`, string(data))
			return "found", nil
		},
		MessageBudgeter: func(messages []proto.Message) ([]proto.Message, error) {
			return append([]proto.Message(nil), messages...), nil
		},
	})

	for st.Next() {
		_, err := st.Current()
		if err != nil {
			require.ErrorIs(t, err, stream.ErrNoContent)
		}
	}
	require.NoError(t, st.Err())
	require.Len(t, st.Messages(), 2)
	require.NotEmpty(t, st.Messages()[1].ProviderData[messagesProviderDataKey])
	require.Len(t, st.CallTools(), 1)

	for st.Next() {
		_, err := st.Current()
		if err != nil {
			require.ErrorIs(t, err, stream.ErrNoContent)
		}
	}
	require.NoError(t, st.Err())
	require.Len(t, requests, 2)

	secondMessages := requests[1]["messages"].([]any)
	require.Len(t, secondMessages, 3)
	assistantContent := secondMessages[1].(map[string]any)["content"].([]any)
	require.Len(t, assistantContent, 3)
	require.Equal(t, "thinking", assistantContent[0].(map[string]any)["type"])
	require.Equal(t, "opaque-signature", assistantContent[0].(map[string]any)["signature"])
	require.Equal(t, "redacted_thinking", assistantContent[1].(map[string]any)["type"])
	require.Equal(t, "encrypted", assistantContent[1].(map[string]any)["data"])
	require.Equal(t, "tool_use", assistantContent[2].(map[string]any)["type"])
	toolResults := secondMessages[2].(map[string]any)["content"].([]any)
	require.Len(t, toolResults, 1)
	require.Equal(t, "tool_result", toolResults[0].(map[string]any)["type"])
	require.Equal(t, "tool_1", toolResults[0].(map[string]any)["tool_use_id"])
}
