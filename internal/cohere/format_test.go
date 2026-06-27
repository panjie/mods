package cohere

import (
	"testing"

	cohere "github.com/cohere-ai/cohere-go/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestFromProtoRole(t *testing.T) {
	require.Equal(t, "SYSTEM", fromProtoRole(proto.RoleSystem))
	require.Equal(t, "CHATBOT", fromProtoRole(proto.RoleAssistant))
	require.Equal(t, "USER", fromProtoRole(proto.RoleUser))
	require.Equal(t, "USER", fromProtoRole("unknown"))
}

func TestFromProtoMessages(t *testing.T) {
	t.Run("single user message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "Hello"},
		}
		history, message := fromProtoMessages(input)
		require.Empty(t, history)
		require.Equal(t, "Hello", message)
	})

	t.Run("system then user", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleSystem, Content: "Be helpful."},
			{Role: proto.RoleUser, Content: "Hi"},
		}
		history, message := fromProtoMessages(input)
		require.Len(t, history, 1)
		require.Equal(t, "SYSTEM", history[0].Role)
		require.Equal(t, "Hi", message)
	})

	t.Run("multiple turns", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "Q1"},
			{Role: proto.RoleAssistant, Content: "A1"},
			{Role: proto.RoleUser, Content: "Q2"},
		}
		history, message := fromProtoMessages(input)
		require.Len(t, history, 2)
		require.Equal(t, "Q2", message)
	})
}

func TestToProtoMessages(t *testing.T) {
	t.Run("user message", func(t *testing.T) {
		input := []*cohere.Message{
			{
				Role: "USER",
				User: &cohere.ChatMessage{Message: "Hello"},
			},
		}
		msgs := toProtoMessages(input)
		require.Len(t, msgs, 1)
		require.Equal(t, proto.RoleUser, msgs[0].Role)
		require.Equal(t, "Hello", msgs[0].Content)
	})

	t.Run("system message", func(t *testing.T) {
		input := []*cohere.Message{
			{
				Role:   "SYSTEM",
				System: &cohere.ChatMessage{Message: "Be helpful."},
			},
		}
		msgs := toProtoMessages(input)
		require.Len(t, msgs, 1)
		require.Equal(t, proto.RoleSystem, msgs[0].Role)
	})

	t.Run("chatbot message", func(t *testing.T) {
		input := []*cohere.Message{
			{
				Role:    "CHATBOT",
				Chatbot: &cohere.ChatMessage{Message: "I'm an AI."},
			},
		}
		msgs := toProtoMessages(input)
		require.Len(t, msgs, 1)
		require.Equal(t, proto.RoleAssistant, msgs[0].Role)
	})
}

// TestFromProtoMessagesTailNotUserDoesNotPanic pins the nil-deref fix for
// the case where the last message is not USER role. mods can legitimately
// produce such a tail through:
//
//   - planning prompt injection (a SYSTEM message appended at the end of
//     the history just before sending the request);
//   - cache replay of a conversation whose final turn was an assistant
//     message resumed under --api cohere.
//
// In both cases the previous implementation dereferenced messages[-1].User
// directly and panicked.
func TestFromProtoMessagesTailNotUserDoesNotPanic(t *testing.T) {
	t.Run("trailing system message", func(t *testing.T) {
		history, message := fromProtoMessages([]proto.Message{
			{Role: proto.RoleUser, Content: "hi"},
			{Role: proto.RoleSystem, Content: "be brief"},
		})
		require.Len(t, history, 1)
		require.Equal(t, "USER", history[0].Role)
		require.Equal(t, "be brief", message)
	})

	t.Run("trailing assistant message", func(t *testing.T) {
		history, message := fromProtoMessages([]proto.Message{
			{Role: proto.RoleUser, Content: "hi"},
			{Role: proto.RoleAssistant, Content: "hello"},
		})
		require.Len(t, history, 1)
		require.Equal(t, "USER", history[0].Role)
		require.Equal(t, "hello", message)
	})

	t.Run("single non-user message", func(t *testing.T) {
		history, message := fromProtoMessages([]proto.Message{
			{Role: proto.RoleSystem, Content: "system only"},
		})
		require.Empty(t, history)
		require.Equal(t, "system only", message)
	})

	t.Run("empty cohere message produces empty content", func(t *testing.T) {
		// Direct unit on the helper: a fully-empty cohere.Message has
		// no populated role field, and the helper must return "" rather
		// than reach for a nil receiver.
		require.Equal(t, "", lastMessageContent(&cohere.Message{}))
		require.Equal(t, "", lastMessageContent(nil))
	})
}

// TestStreamCloseNilStreamNoPanic locks in the nil-deref fix for
// Stream.Close: when c.ChatStream fails, Request leaves s.stream nil but
// the streamRunner.close path in the app layer calls Close
// unconditionally. The fix guards the nil so a request-time failure
// cannot turn into a deferred Close panic.
func TestStreamCloseNilStreamNoPanic(t *testing.T) {
	s := &Stream{}
	require.NotPanics(t, func() {
		require.NoError(t, s.Close())
	})
	require.True(t, s.done, "Close must still mark the stream done")
}
