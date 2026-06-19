package cohere

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	cohere "github.com/cohere-ai/cohere-go/v2"
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
