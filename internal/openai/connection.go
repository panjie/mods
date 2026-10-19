package openai

import (
	"context"
	"errors"
	"fmt"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

// CheckConnection exercises the same request builder and stream decoder as a
// real turn, without tools. A successful HTTP response alone is insufficient.
func CheckConnection(ctx context.Context, cfg Config, model string) error {
	limit := int64(512)
	s := New(cfg).Request(ctx, proto.Request{
		Model: model, MaxTokens: &limit,
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "Reply with only OK."}},
	})
	defer func() { _ = s.Close() }()
	for s.Next() {
		if _, err := s.Current(); err != nil && !errors.Is(err, stream.ErrNoContent) {
			return err
		}
	}
	if err := s.Err(); err != nil {
		return err
	}
	switch s := s.(type) {
	case *responseStream:
		if s.incomplete {
			return fmt.Errorf("connection response incomplete (output limit or provider interruption)")
		}
		if s.terminal == nil || s.terminal.Status != "completed" {
			return fmt.Errorf("connection response did not complete")
		}
	case *Stream:
		if len(s.message.Choices) == 0 || s.message.Choices[0].FinishReason != "stop" {
			return fmt.Errorf("connection response did not finish normally")
		}
	default:
		return fmt.Errorf("connection response has no completion status")
	}
	return nil
}
