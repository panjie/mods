package stream

import (
	"encoding/json"
	"fmt"

	"github.com/panjie/mods/internal/media"
	"github.com/panjie/mods/internal/proto"
)

// ValidateMedia bounds cumulative media, including replayed conversation data.
func ValidateMedia(messages []proto.Message) error { return media.Validate(messages) }

// ValidateMediaWireRequest reserves headroom under provider request limits.
func ValidateMediaWireRequest(messages []proto.Message, request any) error {
	if err := ValidateMedia(messages); err != nil {
		return err
	}
	hasMedia := false
	for _, m := range messages {
		if len(m.Attachments()) > 0 {
			hasMedia = true
			break
		}
	}
	if !hasMedia {
		return nil
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data) > 40*1024*1024 {
		return fmt.Errorf("media request exceeds 40 MiB wire budget; media search is incomplete")
	}
	return nil
}

// Failed returns a stream that reports a validation error without making I/O.
func Failed(err error, messages []proto.Message) Stream {
	return &failedStream{err: err, messages: messages}
}

type failedStream struct {
	err      error
	messages []proto.Message
}

func (s *failedStream) Next() bool                        { return false }
func (s *failedStream) Current() (proto.Chunk, error)     { return proto.Chunk{}, s.err }
func (s *failedStream) Close() error                      { return nil }
func (s *failedStream) Err() error                        { return s.err }
func (s *failedStream) Messages() []proto.Message         { return s.messages }
func (s *failedStream) Usage() proto.TokenUsage           { return proto.TokenUsage{} }
func (s *failedStream) CallTools() []proto.ToolCallStatus { return nil }

// ValidateImages is retained for image-only callers; it validates all media.
func ValidateImages(messages []proto.Message) error { return ValidateMedia(messages) }

// ValidateImageWireRequest is the legacy name for the shared request guard.
func ValidateImageWireRequest(messages []proto.Message, request any) error {
	return ValidateMediaWireRequest(messages, request)
}
