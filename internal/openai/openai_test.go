package openai

import (
	"encoding/json"
	"testing"

	"github.com/openai/openai-go"
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
	t.Run("reasoning_content (DeepSeek / MiniMax)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"the user is asking about X"}`)
		if got := extractThought(d); got != "the user is asking about X" {
			t.Fatalf("expected the thought content, got %q", got)
		}
	})

	t.Run("reasoning (Qwen-style)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning":"reasoning payload"}`)
		if got := extractThought(d); got != "reasoning payload" {
			t.Fatalf("expected reasoning payload, got %q", got)
		}
	})

	t.Run("thinking (Anthropic-compat)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"thinking":"thinking payload"}`)
		if got := extractThought(d); got != "thinking payload" {
			t.Fatalf("expected thinking payload, got %q", got)
		}
	})

	t.Run("thinking_content (alt Anthropic-style)", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"thinking_content":"alt payload"}`)
		if got := extractThought(d); got != "alt payload" {
			t.Fatalf("expected alt payload, got %q", got)
		}
	})

	t.Run("content-only delta (OpenAI native) returns empty", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"content":"hello"}`)
		if got := extractThought(d); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("null reasoning_content is skipped", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":null}`)
		if got := extractThought(d); got != "" {
			t.Fatalf("expected empty string, got %q", got)
		}
	})

	t.Run("priority: reasoning_content wins over reasoning", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"primary","reasoning":"secondary"}`)
		if got := extractThought(d); got != "primary" {
			t.Fatalf("expected primary, got %q", got)
		}
	})

	t.Run("priority: reasoning wins over thinking", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning":"second","thinking":"third"}`)
		if got := extractThought(d); got != "second" {
			t.Fatalf("expected second, got %q", got)
		}
	})

	t.Run("JSON-quoted string is unescaped", func(t *testing.T) {
		d := deltaWithRawJSON(t, `{"reasoning_content":"line1\nline2"}`)
		if got := extractThought(d); got != "line1\nline2" {
			t.Fatalf("expected unescaped string, got %q", got)
		}
	})
}

func TestThinkParser(t *testing.T) {
	// feedAll runs each chunk through one parser and concatenates results.
	feedAll := func(chunks ...string) (content, thought string) {
		var p thinkParser
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
		var p thinkParser
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
