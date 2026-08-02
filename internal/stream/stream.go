// Package stream provides interfaces for streaming conversations.
package stream

import (
	"context"
	"errors"

	"github.com/panjie/mods/internal/proto"
)

// ErrNoContent happens when the client is returning no content.
var ErrNoContent = errors.New("no content")

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
	// FunctionTools and CustomTools distinguish structured JSON function calls
	// from free-form Responses custom tools. Tools remains the compatibility
	// aggregate used by registry setup.
	FunctionTools bool
	CustomTools   bool
	// JSONResponseFormat reports whether the backend accepts a native JSON
	// response-format request. False means callers should rely on prompt-level
	// formatting instructions only.
	JSONResponseFormat bool
	// NativeWebSearch reports support for a provider-hosted Responses search
	// tool that does not require a local web-search backend.
	NativeWebSearch bool
	HostedWebSearch bool
	// CustomApplyPatch reports support for the Codex free-form apply_patch tool.
	CustomApplyPatch bool
	// Images reports whether image input is accepted by the selected endpoint.
	Images bool
	Files  bool
	// Reasoning reports provider-native reasoning support; ReasoningReplay
	// means opaque or plaintext reasoning state can be continued after tools.
	Reasoning       bool
	ReasoningReplay bool
	// StatefulResponses is false for the current clients: mods deliberately
	// replays local history instead of relying on previous_response_id.
	StatefulResponses bool
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

	// cumulative token usage across all model calls in this stream
	Usage() proto.TokenUsage

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
