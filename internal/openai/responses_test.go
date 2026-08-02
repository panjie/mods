package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

func writeResponsesSSE(t *testing.T, w http.ResponseWriter, events ...string) {
	t.Helper()
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	for _, event := range events {
		_, err := io.WriteString(w, "data: "+event+"\n\n")
		require.NoError(t, err)
	}
	_, err := io.WriteString(w, "data: [DONE]\n\n")
	require.NoError(t, err)
}

func completedResponseEvent(output string, inputTokens, outputTokens int) string {
	return `{"type":"response.completed","sequence_number":9,"response":{` +
		`"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"test",` +
		`"output":` + output + `,` +
		`"usage":{"input_tokens":` + jsonNumber(inputTokens) +
		`,"output_tokens":` + jsonNumber(outputTokens) +
		`,"total_tokens":` + jsonNumber(inputTokens+outputTokens) + `}}}`
}

func jsonNumber(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func drainStream(t *testing.T, st stream.Stream) (content, thought string) {
	t.Helper()
	for st.Next() {
		chunk, err := st.Current()
		if errors.Is(err, stream.ErrNoContent) {
			continue
		}
		require.NoError(t, err)
		content += chunk.Content
		thought += chunk.Thought
	}
	require.NoError(t, st.Err())
	return content, thought
}

func TestResponsesRequestWireFormat(t *testing.T) {
	var path string
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		captured, _ = io.ReadAll(r.Body)
		writeResponsesSSE(t, w, completedResponseEvent(
			`[{"id":"msg_1","type":"message","role":"assistant","status":"completed",`+
				`"content":[{"type":"output_text","text":"ok","annotations":[]}]}]`,
			7,
			2,
		))
	}))
	defer server.Close()

	jsonFormat := "json"
	maxTokens := int64(321)
	temperature := 0.25
	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
		ThinkTags:    true,
		ExtraParams: map[string]any{
			"reasoning_effort":     "none",
			"service_tier":         "priority",
			"store":                true,
			"previous_response_id": "resp_forbidden",
			"conversation":         "conv_forbidden",
			"include":              []string{"message.output_text.logprobs"},
			"stream":               false,
		},
	})
	st := client.Request(context.Background(), proto.Request{
		Model:          "gpt-5.4-mini-2026-03-17",
		User:           "stable-user",
		Temperature:    &temperature,
		MaxTokens:      &maxTokens,
		ResponseFormat: &jsonFormat,
		Tools: []proto.ToolSpec{{
			Name:        "lookup",
			Description: "look something up",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "term"},
				},
			},
		}},
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: "system"},
			{
				Role:    proto.RoleUser,
				Content: "hello",
				Images: []proto.Image{{
					Data:     []byte("image"),
					MimeType: "image/png",
				}},
			},
		},
	})
	content, _ := drainStream(t, st)
	require.Empty(t, content)
	require.Equal(t, "ok", st.Messages()[2].Content)
	require.Equal(t, "/responses", path)

	var body map[string]any
	require.NoError(t, json.Unmarshal(captured, &body))
	require.Equal(t, false, body["store"])
	require.Equal(t, true, body["stream"])
	require.Equal(t, "priority", body["service_tier"])
	require.Equal(t, "stable-user", body["user"])
	require.Equal(t, float64(321), body["max_output_tokens"])
	require.Equal(t, 0.25, body["temperature"])
	require.NotContains(t, body, "reasoning_effort")
	require.NotContains(t, body, "previous_response_id")
	require.NotContains(t, body, "conversation")

	reasoning := body["reasoning"].(map[string]any)
	require.Equal(t, "none", reasoning["effort"])
	require.Equal(t, "auto", reasoning["summary"])
	require.Equal(t, []any{"reasoning.encrypted_content"}, body["include"])
	textConfig := body["text"].(map[string]any)
	require.Equal(t, "json_object", textConfig["format"].(map[string]any)["type"])

	tools := body["tools"].([]any)
	tool := tools[0].(map[string]any)
	require.Equal(t, "function", tool["type"])
	require.Equal(t, "lookup", tool["name"])
	require.Equal(t, false, tool["strict"])
	require.NotContains(t, tool, "function")

	input := body["input"].([]any)
	require.Equal(t, "system", input[0].(map[string]any)["role"])
	userContent := input[1].(map[string]any)["content"].([]any)
	require.Equal(t, "input_image", userContent[0].(map[string]any)["type"])
	require.Equal(t, "data:image/png;base64,aW1hZ2U=", userContent[0].(map[string]any)["image_url"])
	require.Equal(t, "input_text", userContent[1].(map[string]any)["type"])
}

func TestResponsesStreamingContentThoughtRefusalAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponsesSSE(t, w,
			`{"type":"response.reasoning_summary_text.delta","delta":"think","item_id":"rs_1","output_index":0,"summary_index":0,"sequence_number":1}`,
			`{"type":"response.output_text.delta","delta":"hello","item_id":"msg_1","output_index":1,"content_index":0,"sequence_number":2,"logprobs":[]}`,
			`{"type":"response.refusal.delta","delta":" no","item_id":"msg_1","output_index":1,"content_index":1,"sequence_number":3}`,
			completedResponseEvent(
				`[{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[`+
					`{"type":"output_text","text":"hello","annotations":[]},{"type":"refusal","refusal":" no"}]}]`,
				10,
				4,
			),
		)
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:      "test",
		TrackUsage: true,
		Messages:   []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	content, thought := drainStream(t, st)
	require.Equal(t, "hello no", content)
	require.Equal(t, "think", thought)
	require.Equal(t, proto.TokenUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}, st.Usage())
	require.Len(t, st.Messages(), 2)
	require.Equal(t, "hello no", st.Messages()[1].Content)
	require.NotEmpty(t, st.Messages()[1].ProviderData[openAIResponsesProviderDataKey])
}

func TestDeepSeekResponsesWireFormat(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		writeResponsesSSE(t, w, completedResponseEvent(`[]`, 7, 2))
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:          server.URL,
		HTTPClient:       server.Client(),
		AuthToken:        "k",
		UseResponses:     true,
		ResponsesProfile: ResponsesProfileDeepSeek,
		ThinkTags:        true,
		ExtraParams: map[string]any{
			"reasoning_effort":     "minimal",
			"include":              []string{"reasoning.encrypted_content"},
			"store":                true,
			"previous_response_id": "ignored",
			"service_tier":         "priority",
			"background":           true,
		},
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "deepseek-v4-flash",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
		Tools: []proto.ToolSpec{
			{Name: "fs_apply_patch", WireName: "apply_patch", WireType: "custom"},
			{Name: "web_search", WireType: "web_search"},
		},
	})
	_, _ = drainStream(t, st)

	var body map[string]any
	require.NoError(t, json.Unmarshal(captured, &body))
	require.NotContains(t, body, "store")
	require.NotContains(t, body, "include")
	require.NotContains(t, body, "previous_response_id")
	require.NotContains(t, body, "service_tier")
	require.NotContains(t, body, "background")
	reasoning := body["reasoning"].(map[string]any)
	require.Equal(t, "low", reasoning["effort"])
	require.NotContains(t, reasoning, "summary")
	tools := body["tools"].([]any)
	require.Equal(t, map[string]any{"type": "custom", "name": "apply_patch"}, tools[0])
	require.Equal(t, map[string]any{"type": "web_search"}, tools[1])
}

func TestDeepSeekResponsesReasoningSearchSourcesAndUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.reasoning_text.delta","delta":"reason","item_id":"rs_1","output_index":0,"content_index":0,"sequence_number":1}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.web_search_call.searching","item_id":"ws_1","output_index":1,"sequence_number":2}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.output_text.delta","delta":"answer","item_id":"msg_1","output_index":2,"content_index":0,"sequence_number":3,"logprobs":[]}`+"\n\n")
		_, _ = io.WriteString(w, "data: "+`{"type":"response.completed","sequence_number":4,"response":{"id":"resp_1","object":"response","created_at":0,"status":"completed","model":"deepseek-v4-flash","output":[{"id":"ws_1","type":"web_search_call","status":"completed"},{"id":"msg_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"answer","annotations":[{"type":"url_citation","url":"https://example.com/a","title":"Example"},{"type":"url_citation","url":"https://example.com/a","title":"Duplicate"}]}]}],"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":4},"output_tokens":6,"output_tokens_details":{"reasoning_tokens":3},"total_tokens":16}}}`+"\n\n")
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k", UseResponses: true, ResponsesProfile: ResponsesProfileDeepSeek})
	st := client.Request(context.Background(), proto.Request{
		Model: "deepseek-v4-flash", TrackUsage: true,
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	content, thought := drainStream(t, st)
	require.Equal(t, "reason", thought)
	require.Equal(t, "answer\n\nSources:\n- [Example](https://example.com/a)", content)
	require.Equal(t, proto.TokenUsage{
		InputTokens: 10, CachedInputTokens: 4, OutputTokens: 6,
		ReasoningOutputTokens: 3, TotalTokens: 16,
	}, st.Usage())
	require.Contains(t, st.Messages()[1].Content, "Sources:")
	require.NotEmpty(t, st.Messages()[1].ProviderData[deepSeekResponsesProviderDataKey])
	require.Empty(t, st.Messages()[1].ProviderData[openAIResponsesProviderDataKey])
}

func TestLegacyResponsesProviderDataReplaysUnderResolvedProfile(t *testing.T) {
	state := json.RawMessage(`[{"id":"rs_1","type":"reasoning","status":"completed","summary":[],"encrypted_content":"opaque"}]`)
	input, err := fromProtoResponseInput([]proto.Message{{
		Role:         proto.RoleAssistant,
		ProviderData: map[string]json.RawMessage{legacyResponsesProviderDataKey: state},
	}}, ProviderProfileOpenAI)
	require.NoError(t, err)
	require.Len(t, input, 1)
}

func TestDeepSeekResponsesCustomApplyPatchRound(t *testing.T) {
	var bodies [][]byte
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, bytes.Clone(body))
		requestCount++
		if requestCount == 1 {
			writeResponsesSSE(t, w, completedResponseEvent(
				`[{"id":"rs_1","type":"reasoning","status":"completed","summary":[],"content":[{"type":"reasoning_text","text":"plain reasoning"}]},{"id":"ct_1","type":"custom_tool_call","status":"completed","call_id":"call_1","name":"apply_patch","input":"*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch"}]`,
				8, 3,
			))
			return
		}
		writeResponsesSSE(t, w,
			`{"type":"response.output_text.delta","delta":"done","item_id":"msg_2","output_index":0,"content_index":0,"sequence_number":1,"logprobs":[]}`,
			completedResponseEvent(`[{"id":"msg_2","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"done","annotations":[]}]}]`, 12, 2),
		)
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k", UseResponses: true, ResponsesProfile: ResponsesProfileDeepSeek})
	st := client.Request(context.Background(), proto.Request{
		Model:    "deepseek-v4-flash",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "edit"}},
		Tools:    []proto.ToolSpec{{Name: "fs_apply_patch", WireName: "apply_patch", WireType: "custom"}},
		ToolCaller: func(name string, data []byte) (string, error) {
			require.Equal(t, "fs_apply_patch", name)
			require.JSONEq(t, `{"patch":"*** Begin Patch\n*** Add File: a.txt\n+x\n*** End Patch"}`, string(data))
			return "Patch applied", nil
		},
	})
	content, _ := drainStream(t, st)
	require.Empty(t, content)
	statuses := st.CallTools()
	require.Len(t, statuses, 1)
	require.Equal(t, "fs_apply_patch", statuses[0].Name)
	content, _ = drainStream(t, st)
	require.Equal(t, "done", content)
	require.Equal(t, 2, requestCount)

	var followup map[string]any
	require.NoError(t, json.Unmarshal(bodies[1], &followup))
	input := followup["input"].([]any)
	require.Equal(t, "reasoning", input[1].(map[string]any)["type"])
	require.Equal(t, "plain reasoning", input[1].(map[string]any)["content"].([]any)[0].(map[string]any)["text"])
	require.Equal(t, "custom_tool_call", input[2].(map[string]any)["type"])
	require.Equal(t, "custom_tool_call_output", input[3].(map[string]any)["type"])
	require.Equal(t, "call_1", input[3].(map[string]any)["call_id"])
	require.Equal(t, "Patch applied", input[3].(map[string]any)["output"])
}

func TestDeepSeekResponsesRejectsImagesBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { requests++ }))
	defer server.Close()
	client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k", UseResponses: true, ResponsesProfile: ResponsesProfileDeepSeek})
	st := client.Request(context.Background(), proto.Request{
		Model:    "deepseek-v4-flash",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "see", Images: []proto.Image{{Data: []byte("x"), MimeType: "image/png"}}}},
	})
	require.False(t, st.Next())
	require.ErrorContains(t, st.Err(), "does not support image")
	require.Zero(t, requests)
}

func TestResponsesToolRoundReplaysEncryptedReasoning(t *testing.T) {
	var bodies [][]byte
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		bodies = append(bodies, bytes.Clone(body))
		requestCount++
		if requestCount == 1 {
			writeResponsesSSE(t, w, completedResponseEvent(
				`[`+
					`{"id":"rs_1","type":"reasoning","status":"completed","summary":[],"encrypted_content":"encrypted"},`+
					`{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"lookup","arguments":"{\"query\":\"mods\"}"},`+
					`{"id":"fc_2","type":"function_call","status":"completed","call_id":"call_2","name":"lookup_more","arguments":"{\"query\":\"responses\"}"}`+
					`]`,
				8,
				3,
			))
			return
		}
		writeResponsesSSE(t, w,
			`{"type":"response.output_text.delta","delta":"done","item_id":"msg_2","output_index":0,"content_index":0,"sequence_number":1,"logprobs":[]}`,
			completedResponseEvent(
				`[{"id":"msg_2","type":"message","role":"assistant","status":"completed",`+
					`"content":[{"type":"output_text","text":"done","annotations":[]}]}]`,
				15,
				2,
			),
		)
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:      "gpt-5.4-mini-2026-03-17",
		TrackUsage: true,
		Tools: []proto.ToolSpec{{
			Name:        "lookup",
			InputSchema: map[string]any{"type": "object"},
		}, {
			Name:        "lookup_more",
			InputSchema: map[string]any{"type": "object"},
		}},
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
		ToolCaller: func(name string, data []byte) (string, error) {
			switch name {
			case "lookup":
				require.JSONEq(t, `{"query":"mods"}`, string(data))
				return "tool output", nil
			case "lookup_more":
				require.JSONEq(t, `{"query":"responses"}`, string(data))
				return "more output", nil
			default:
				return "", errors.New("unexpected tool")
			}
		},
	})

	content, _ := drainStream(t, st)
	require.Empty(t, content)
	statuses := st.CallTools()
	require.Len(t, statuses, 2)
	require.Equal(t, "tool output", statuses[0].Output)
	require.Equal(t, "more output", statuses[1].Output)

	content, _ = drainStream(t, st)
	require.Equal(t, "done", content)
	require.Equal(t, 2, requestCount)
	require.Equal(t, proto.TokenUsage{InputTokens: 23, OutputTokens: 5, TotalTokens: 28}, st.Usage())

	var followup map[string]any
	require.NoError(t, json.Unmarshal(bodies[1], &followup))
	input := followup["input"].([]any)
	require.Len(t, input, 6)
	require.Equal(t, "user", input[0].(map[string]any)["role"])
	require.Equal(t, "reasoning", input[1].(map[string]any)["type"])
	require.Equal(t, "encrypted", input[1].(map[string]any)["encrypted_content"])
	require.Equal(t, "function_call", input[2].(map[string]any)["type"])
	require.Equal(t, "call_1", input[2].(map[string]any)["call_id"])
	require.Equal(t, "function_call", input[3].(map[string]any)["type"])
	require.Equal(t, "call_2", input[3].(map[string]any)["call_id"])
	require.Equal(t, "function_call_output", input[4].(map[string]any)["type"])
	require.Equal(t, "call_1", input[4].(map[string]any)["call_id"])
	require.Equal(t, "tool output", input[4].(map[string]any)["output"])
	require.Equal(t, "function_call_output", input[5].(map[string]any)["type"])
	require.Equal(t, "call_2", input[5].(map[string]any)["call_id"])
	require.Equal(t, "more output", input[5].(map[string]any)["output"])
}

func TestResponsesIncompleteDoesNotExposeToolCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponsesSSE(t, w,
			`{"type":"response.output_text.delta","delta":"partial","item_id":"msg_1","output_index":0,"content_index":0,"sequence_number":1,"logprobs":[]}`,
			`{"type":"response.incomplete","sequence_number":2,"response":{`+
				`"id":"resp_1","object":"response","created_at":0,"status":"incomplete","model":"test",`+
				`"incomplete_details":{"reason":"max_output_tokens"},`+
				`"output":[{"id":"fc_1","type":"function_call","status":"completed","call_id":"call_1","name":"lookup","arguments":"{}"}],`+
				`"usage":{"input_tokens":4,"output_tokens":2,"total_tokens":6}}}`,
		)
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "test",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	content, _ := drainStream(t, st)
	require.Equal(t, "partial", content)
	require.Empty(t, st.CallTools())
	require.Len(t, st.Messages(), 2)
	require.Empty(t, st.Messages()[1].ToolCalls)
	require.Empty(t, st.Messages()[1].ProviderData)
}

func TestResponsesFailureBecomesStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponsesSSE(t, w,
			`{"type":"response.failed","sequence_number":1,"response":{`+
				`"id":"resp_1","object":"response","created_at":0,"status":"failed","model":"test","output":[],`+
				`"error":{"code":"server_error","message":"boom"}}}`,
		)
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "test",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	for st.Next() {
		_, _ = st.Current()
	}
	require.ErrorContains(t, st.Err(), "boom")
}

func TestResponsesErrorEventBecomesStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeResponsesSSE(t, w,
			`{"type":"error","sequence_number":1,"code":"invalid_request","message":"bad input","param":"input"}`,
		)
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "test",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	for st.Next() {
		_, _ = st.Current()
	}
	require.ErrorContains(t, st.Err(), "bad input")
}

func TestResponsesEarlyEndBecomesStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:      server.URL,
		HTTPClient:   server.Client(),
		AuthToken:    "k",
		UseResponses: true,
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "test",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	require.False(t, st.Next())
	require.ErrorIs(t, st.Err(), errResponsesStreamEnded)
}

func TestChatCompletionsPathRemainsAvailable(t *testing.T) {
	var path string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "k",
	})
	st := client.Request(context.Background(), proto.Request{
		Model:    "custom-model",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	for st.Next() {
		_, _ = st.Current()
	}
	require.NoError(t, st.Err())
	require.Equal(t, "/chat/completions", path)
}
