package ollama

import (
	"testing"
	"time"

	"github.com/ollama/ollama/api"
	"github.com/panjie/mods/internal/proto"
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
