package google

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestStreamMessagesIncludesAssistantResponse(t *testing.T) {
	stream := &Stream{
		messages: []proto.Message{
			{Role: proto.RoleUser, Content: "hello"},
		},
		message: "hi there",
	}

	require.Equal(t, []proto.Message{
		{Role: proto.RoleUser, Content: "hello"},
		{Role: proto.RoleAssistant, Content: "hi there"},
	}, stream.Messages())
}
