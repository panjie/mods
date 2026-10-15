package stream

import (
	"encoding/json"
	"fmt"

	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/proto"
)

// ValidateImages bounds cumulative images, including replayed conversation data.
func ValidateImages(messages []proto.Message) error {
	count, bytes := 0, 0
	for _, m := range messages {
		for _, img := range m.Images {
			count++
			bytes += len(img.Data)
		}
	}
	if count > imageutil.MaxImageCount || bytes > imageutil.MaxTotalImageBytes {
		return fmt.Errorf("image context budget exceeded (%d images, %d bytes); start a smaller image batch", count, bytes)
	}
	return nil
}

// ValidateImageWireRequest reserves headroom under provider request limits.
func ValidateImageWireRequest(messages []proto.Message, request any) error {
	if err := ValidateImages(messages); err != nil {
		return err
	}
	hasImages := false
	for _, m := range messages {
		if len(m.Images) > 0 {
			hasImages = true
			break
		}
	}
	if !hasImages {
		return nil
	}
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data) > 40*1024*1024 {
		return fmt.Errorf("image request exceeds 40 MiB wire budget; image search is incomplete")
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
