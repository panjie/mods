package ollama

import (
	"encoding/json"
	"testing"
	"time"

	api "github.com/panjie/mods/internal/ollamaapi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestNextStopsAtCompletedMessage(t *testing.T) {
	s := &Stream{
		done: true,
	}

	if s.Next() {
		t.Fatal("expected completed stream to return false")
	}
	if len(s.messages) != 1 {
		t.Fatalf("expected assistant message appended, got %d", len(s.messages))
	}
	if len(s.request.Messages) != 1 {
		t.Fatalf("expected request message appended, got %d", len(s.request.Messages))
	}
}

func TestNewChatRequestOptions(t *testing.T) {
	maxTokens := int64(123)
	temp := 0.7
	req := newChatRequest(proto.Request{
		Model:       "llama",
		MaxTokens:   &maxTokens,
		Temperature: &temp,
	})

	if got := req.Options["num_predict"]; got != maxTokens {
		t.Fatalf("expected num_predict=%d, got %#v", maxTokens, got)
	}
	if _, ok := req.Options["num_ctx"]; ok {
		t.Fatal("max tokens should not be mapped to num_ctx")
	}
	if got := req.Options["temperature"]; got != temp {
		t.Fatalf("expected temperature=%v, got %#v", temp, got)
	}
}

func TestNewChatRequestIncludesThinkValue(t *testing.T) {
	require.Equal(t, false, newChatRequest(proto.Request{Model: "llama"}, false).Think)
	require.Equal(t, true, newChatRequest(proto.Request{Model: "llama"}, true).Think)
	require.Equal(t, "high", newChatRequest(proto.Request{Model: "llama"}, "high").Think)
	wire, err := json.Marshal(newChatRequest(proto.Request{Model: "llama"}, false))
	require.NoError(t, err)
	require.Contains(t, string(wire), `"think":false`)
}

func TestCallToolsPreservesCallMetadata(t *testing.T) {
	s := &Stream{
		message: api.Message{ToolCalls: []api.ToolCall{{Function: api.ToolCallFunction{
			Index: 7, Name: "lookup", Arguments: api.ToolCallFunctionArguments{"q": "mods"},
		}}}},
		toolCall: func(call proto.ToolCallRequest) (string, error) {
			require.Equal(t, "7", call.ID)
			return "found", nil
		},
		factory: func() {},
	}

	statuses := s.CallTools()
	require.Len(t, statuses, 1)
	require.Equal(t, "7", statuses[0].ID)
	require.Equal(t, 1, statuses[0].Index)
	require.Equal(t, 1, statuses[0].Total)
}

func TestCurrentBlocksUntilResponse(t *testing.T) {
	s := &Stream{respCh: make(chan api.ChatResponse, 1)}
	done := make(chan proto.Chunk, 1)
	go func() {
		chunk, err := s.Current()
		if err != nil {
			t.Errorf("Current returned error before response: %v", err)
			return
		}
		done <- chunk
	}()

	select {
	case <-done:
		t.Fatal("Current returned before a response was available")
	case <-time.After(20 * time.Millisecond):
	}

	s.respCh <- api.ChatResponse{Message: api.Message{Content: "hello"}}
	select {
	case chunk := <-done:
		if chunk.Content != "hello" {
			t.Fatalf("unexpected chunk: %#v", chunk)
		}
	case <-time.After(time.Second):
		t.Fatal("Current did not return after response")
	}
}

func TestCurrentRoutesOllamaThinkingSeparately(t *testing.T) {
	s := &Stream{respCh: make(chan api.ChatResponse, 1)}
	s.respCh <- api.ChatResponse{Message: api.Message{Content: "answer", Thinking: "reason"}}
	chunk, err := s.Current()
	require.NoError(t, err)
	require.Equal(t, "answer", chunk.Content)
	require.Equal(t, "reason", chunk.Thought)
	require.Equal(t, "reason", s.message.Thinking)
}

func TestCurrentCollectsFinalTokenUsage(t *testing.T) {
	s := &Stream{respCh: make(chan api.ChatResponse, 1), trackUsage: true}
	s.respCh <- api.ChatResponse{
		Done:    true,
		Metrics: api.Metrics{PromptEvalCount: 11, EvalCount: 6},
	}
	_, err := s.Current()
	if err != nil {
		t.Fatalf("Current returned error: %v", err)
	}
	want := proto.TokenUsage{InputTokens: 11, OutputTokens: 6, TotalTokens: 17}
	if got := s.Usage(); got != want {
		t.Fatalf("Usage() = %#v, want %#v", got, want)
	}
}
