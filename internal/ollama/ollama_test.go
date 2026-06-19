package ollama

import (
	"reflect"
	"testing"

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
	topP := 0.9
	req := newChatRequest(proto.Request{
		Model:       "llama",
		Stop:        []string{"END", "STOP"},
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
	})

	if got := req.Options["num_predict"]; got != maxTokens {
		t.Fatalf("expected num_predict=%d, got %#v", maxTokens, got)
	}
	if _, ok := req.Options["num_ctx"]; ok {
		t.Fatal("max tokens should not be mapped to num_ctx")
	}
	if got := req.Options["stop"]; !reflect.DeepEqual(got, []string{"END", "STOP"}) {
		t.Fatalf("expected all stop sequences, got %#v", got)
	}
	if got := req.Options["temperature"]; got != temp {
		t.Fatalf("expected temperature=%v, got %#v", temp, got)
	}
	if got := req.Options["top_p"]; got != topP {
		t.Fatalf("expected top_p=%v, got %#v", topP, got)
	}
}
