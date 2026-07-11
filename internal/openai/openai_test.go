package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openai/openai-go"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestPendingToolCallsEmptyChoices(t *testing.T) {
	var s Stream
	if calls := s.pendingToolCalls(); len(calls) != 0 {
		t.Fatalf("expected no calls, got %#v", calls)
	}
	if statuses := s.CallTools(); len(statuses) != 0 {
		t.Fatalf("expected no statuses, got %#v", statuses)
	}
}

// deltaWithRawJSON unmarshals a raw chunk delta JSON into a
// ChatCompletionChunkChoiceDelta. The openai-go SDK captures the raw JSON
// in Delta.JSON.raw, accessible via Delta.RawJSON().
func deltaWithRawJSON(t *testing.T, raw string) openai.ChatCompletionChunkChoiceDelta {
	t.Helper()
	var choice openai.ChatCompletionChunkChoice
	if err := json.Unmarshal([]byte(`{"delta":`+raw+`}`), &choice); err != nil {
		t.Fatalf("unmarshal choice: %v", err)
	}
	return choice.Delta
}

func TestExtractThought(t *testing.T) {
	// defaultStream creates a Stream with the default thought fields.
	defaultStream := func() *Stream { return &Stream{} }

	t.Run("reasoning_content (DeepSeek / GLM)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"the user is asking about X"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "the user is asking about X" {
			t.Fatalf("expected the thought content, got %q", got)
		}
	})

	t.Run("reasoning (Qwen-style)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning":"reasoning payload"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "reasoning payload" {
			t.Fatalf("expected reasoning payload, got %q", got)
		}
	})

	t.Run("thinking (Anthropic-compat)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"thinking":"thinking payload"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "thinking payload" {
			t.Fatalf("expected thinking payload, got %q", got)
		}
	})

	t.Run("thinking_content (alt Anthropic-style)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"thinking_content":"alt payload"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "alt payload" {
			t.Fatalf("expected alt payload, got %q", got)
		}
	})

	t.Run("content-only delta (OpenAI native) returns empty", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"content":"hello"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("null reasoning_content is skipped", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":null}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("priority: reasoning_content wins over reasoning", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"primary","reasoning":"secondary"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "primary" {
			t.Fatalf("expected primary, got %q", got)
		}
	})

	t.Run("priority: reasoning wins over thinking", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning":"second","thinking":"third"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "second" {
			t.Fatalf("expected second, got %q", got)
		}
	})

	t.Run("JSON-quoted string is unescaped", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"line1\nline2"}`)
		s := defaultStream()
		if got := s.extractThought(d); got != "line1\nline2" {
			t.Fatalf("expected unescaped string, got %q", got)
		}
	})

	t.Run("custom thought-fields override defaults", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"default","my_custom_field":"custom"}`)
		s := &Stream{thoughtFields: []string{"my_custom_field"}}
		if got := s.extractThought(d); got != "custom" {
			t.Fatalf("expected custom field value, got %q", got)
		}
	})

	t.Run("custom thought-fields ignore default fields not in list", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"should-be-ignored"}`)
		s := &Stream{thoughtFields: []string{"my_custom_field"}}
		if got := s.extractThought(d); got != "" {
			t.Fatalf("expected empty (custom field absent, default field ignored), got %q", got)
		}
	})
}

func TestThinkParser(t *testing.T) {
	// newDefaultParser creates a thinkParser with the default <think> tag.
	newDefaultParser := func() thinkParser {
		return thinkParser{openTag: "<think>", closeTag: "</think>"}
	}
	// feedAll runs each chunk through one parser and concatenates results.
	feedAll := func(chunks ...string) (content, thought string) {
		p := newDefaultParser()
		for _, c := range chunks {
			gotC, gotT := p.feed(c)
			content += gotC
			thought += gotT
		}
		return content, thought
	}

	t.Run("MiniMax-style think block split across chunks", func(t *testing.T) {
		// Reproduces the real MiniMax stream the user captured.
		content, thought := feedAll(
			"<think>\n用户只是",
			`打了个招呼 "hello"，这是一个简单的问候。我应该用中文简洁地回应，保持友好。`+"\n</think>\n你好！有什么我可以帮",
			"你的吗？",
		)
		wantThought := "\n用户只是" + `打了个招呼 "hello"，这是一个简单的问候。我应该用中文简洁地回应，保持友好。` + "\n"
		wantContent := "\n你好！有什么我可以帮你的吗？"
		if thought != wantThought {
			t.Fatalf("thought:\n got %q\nwant %q", thought, wantThought)
		}
		if content != wantContent {
			t.Fatalf("content:\n got %q\nwant %q", content, wantContent)
		}
	})

	t.Run("no think tags passes everything through as content", func(t *testing.T) {
		content, thought := feedAll("hello ", "world")
		if content != "hello world" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "" {
			t.Fatalf("thought: expected empty, got %q", thought)
		}
	})

	t.Run("open tag split across chunk boundary", func(t *testing.T) {
		content, thought := feedAll("before<thi", "nk>secret</think>after")
		if content != "beforeafter" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "secret" {
			t.Fatalf("thought: got %q", thought)
		}
	})

	t.Run("close tag split across chunk boundary", func(t *testing.T) {
		content, thought := feedAll("<think>secret</thi", "nk>answer")
		if content != "answer" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "secret" {
			t.Fatalf("thought: got %q", thought)
		}
	})

	t.Run("single character at a time", func(t *testing.T) {
		p := newDefaultParser()
		var content, thought string
		for _, r := range "ab<think>cd</think>ef" {
			c, th := p.feed(string(r))
			content += c
			thought += th
		}
		if content != "abef" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "cd" {
			t.Fatalf("thought: got %q", thought)
		}
	})

	t.Run("custom tag name (e.g. <reasoning>)", func(t *testing.T) {
		p := thinkParser{openTag: "<reasoning>", closeTag: "</reasoning>"}
		var content, thought string
		for _, chunk := range []string{"before<reasoning>", "inner", "</reasoning>after"} {
			c, th := p.feed(chunk)
			content += c
			thought += th
		}
		if content != "beforeafter" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "inner" {
			t.Fatalf("thought: got %q", thought)
		}
	})

	t.Run("less-than sign that is not a tag is preserved", func(t *testing.T) {
		content, thought := feedAll("a < b and c < d")
		if content != "a < b and c < d" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "" {
			t.Fatalf("thought: got %q", thought)
		}
	})

	t.Run("text resembling but not matching the tag is preserved", func(t *testing.T) {
		content, thought := feedAll("use the <thinking> element")
		if content != "use the <thinking> element" {
			t.Fatalf("content: got %q", content)
		}
		if thought != "" {
			t.Fatalf("thought: got %q", thought)
		}
	})
}

func TestPartialTagSuffixLen(t *testing.T) {
	cases := []struct {
		s, tag string
		want   int
	}{
		{"hello<thi", "<think>", 4},
		{"hello", "<think>", 0},
		{"abc<", "<think>", 1},
		{"a<b", "<think>", 0},
		{"done</thi", "</think>", 5},
		{"<think", "<think>", 6},
		{"", "<think>", 0},
	}
	for _, c := range cases {
		if got := partialTagSuffixLen(c.s, c.tag); got != c.want {
			t.Errorf("partialTagSuffixLen(%q, %q) = %d, want %d", c.s, c.tag, got, c.want)
		}
	}
}

// newCapturingServer starts a fake SSE server that records the request body
// and immediately ends the stream, returning a capturing openai client.
func newCapturingServer(t *testing.T) (client *Client, captured *[]byte, closeServer func()) {
	t.Helper()
	var buf []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	cfg := Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "k",
	}
	return New(cfg), &buf, server.Close
}

// TestResponseFormatEmittedForNonOpenAIAPI asserts that the JSON
// response_format is sent in the wire body for any OpenAI-compatible
// provider (not just api=="openai"). DeepSeek, Azure, and other
// OpenAI-compatible endpoints support response_format=json_object.
func TestResponseFormatEmittedForNonOpenAIAPI(t *testing.T) {
	jsonFmt := "json"
	client, captured, closeServer := newCapturingServer(t)
	defer closeServer()

	_ = client.Request(context.Background(), proto.Request{
		API:            "deepseek",
		Model:          "deepseek-chat",
		ResponseFormat: &jsonFmt,
		Messages:       []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.NotEmpty(t, *captured, "no request body was captured")
	require.True(t, bytes.Contains(*captured, []byte(`"response_format"`)),
		"wire JSON must contain response_format for non-openai API: %s", *captured)

	var body map[string]any
	require.NoError(t, json.Unmarshal(*captured, &body))
	rf, ok := body["response_format"].(map[string]any)
	require.True(t, ok, "response_format must be an object")
	require.Equal(t, "json_object", rf["type"],
		"response_format.type must be json_object")
}

func TestResponseFormatOmittedWhenNotJSON(t *testing.T) {
	client, captured, closeServer := newCapturingServer(t)
	defer closeServer()

	_ = client.Request(context.Background(), proto.Request{
		API:      "deepseek",
		Model:    "deepseek-chat",
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.NotEmpty(t, *captured, "no request body was captured")
	require.False(t, bytes.Contains(*captured, []byte(`"response_format"`)),
		"wire JSON must not contain response_format when unset: %s", *captured)
	require.NotContains(t, string(*captured), `"stream_options"`,
		"usage tracking must not change requests unless explicitly enabled")
}

func TestStreamingTokenUsage(t *testing.T) {
	var captured []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w,
			"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hello\"},\"finish_reason\":\"stop\"}]}\n\n"+
				"data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":0,\"model\":\"test\",\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":4,\"total_tokens\":14}}\n\n"+
				"data: [DONE]\n\n")
	}))
	defer server.Close()

	client := New(Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "k"})
	st := client.Request(context.Background(), proto.Request{
		Model: "test", TrackUsage: true,
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})
	var content string
	for st.Next() {
		chunk, err := st.Current()
		if err == nil {
			content += chunk.Content
		}
	}
	require.NoError(t, st.Err())
	require.Equal(t, "hello", content, "usage-only chunk must not become response content")
	require.Equal(t, proto.TokenUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14}, st.Usage())
	require.Contains(t, string(captured), `"stream_options":{"include_usage":true}`)
}
