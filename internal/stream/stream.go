// Package stream provides interfaces for streaming conversations.
package stream

import (
	"context"
	"errors"
	"fmt"

	"github.com/panjie/mods/internal/proto"
)

// ErrNoContent happens when the client is returning no content.
var ErrNoContent = errors.New("no content")

const maxToolResultChars = 25000

// Client is a streaming client.
type Client interface {
	Request(context.Context, proto.Request) Stream
	// Capabilities reports what the provider backend supports. The
	// caller uses this to decide whether to register tools, attach
	// images, or fall back to text-only behavior. Implementations
	// must be free of side effects so the method can be invoked
	// before Request without affecting later calls.
	Capabilities() Capabilities
}

// Capabilities describes what a provider backend supports. The zero
// value is the safest fallback (no tools, no images), so unsupported
// features fail closed.
type Capabilities struct {
	// Tools reports whether the provider implements tool/function
	// calling. When false, Stream.CallTools returns nil without
	// invoking any caller, and the registry construction path skips
	// tool registration rather than sending tool specs the backend
	// cannot honor.
	Tools bool
}

// Stream is an ongoing stream.
type Stream interface {
	// returns false when no more messages, caller should run [Stream.CallTools()]
	// once that happens, and then check for this again
	Next() bool

	// the current chunk
	// implementation should accumulate chunks into a message, and keep its
	// internal conversation state
	Current() (proto.Chunk, error)

	// closes the underlying stream
	Close() error

	// streaming error
	Err() error

	// the whole conversation
	Messages() []proto.Message

	// handles any pending tool calls
	CallTools() []proto.ToolCallStatus
}

// CallTool calls a tool using the provided data and caller, and returns the
// resulting [proto.Message] and [proto.ToolCallStatus].
func CallTool(
	id, name string,
	data []byte,
	caller func(name string, data []byte) (string, error),
) (proto.Message, proto.ToolCallStatus) {
	content, err := caller(name, data)
	if content == "" && err != nil {
		content = err.Error()
	}
	if len(content) > maxToolResultChars {
		content = content[:maxToolResultChars] +
			fmt.Sprintf("\n\n[Output truncated at %d chars. Use more specific tools (e.g. list_directory instead of directory_tree, or read_file for single files) to retrieve targeted content.]",
				maxToolResultChars)
	}
	return proto.Message{
			Role:    proto.RoleTool,
			Content: content,
			ToolCalls: []proto.ToolCall{
				{
					ID:      id,
					IsError: err != nil,
					Function: proto.Function{
						Name:      name,
						Arguments: data,
					},
				},
			},
		},
		proto.ToolCallStatus{
			Name:      name,
			Arguments: data,
			Output:    content,
			Err:       err,
		}
}
