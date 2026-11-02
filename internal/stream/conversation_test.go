package stream

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func toolAssistant() proto.Message {
	return proto.Message{Role: proto.RoleAssistant, ProviderData: map[string]json.RawMessage{"opaque": json.RawMessage(`{"id":"saved"}`)}, ToolCalls: []proto.ToolCall{
		{ID: "a", Function: proto.Function{Name: "write", Arguments: json.RawMessage(`{}`)}},
		{ID: "b", Function: proto.Function{Name: "read", Arguments: json.RawMessage(`{}`)}},
	}}
}

func TestConversationCommitsBeforeFollowupAndDoesNotReplay(t *testing.T) {
	requests, calls := 0, 0
	input := []proto.Message{{Role: proto.RoleUser, Content: "work"}}
	failure := errors.New("followup unavailable")
	c := NewConversation(context.Background(), proto.Request{Messages: input, ToolCaller: func(proto.ToolCallRequest) (string, error) {
		calls++
		return "done", nil
	}}, func(_ context.Context, r proto.Request) Round {
		requests++
		require.Nil(t, r.ToolCaller)
		require.Nil(t, r.ResultCaller)
		if requests == 1 {
			return Failed(nil, append(r.Messages, toolAssistant()))
		}
		require.Len(t, r.Messages, 4)
		require.Equal(t, "done", r.Messages[2].Content)
		return Failed(failure, r.Messages)
	})
	require.False(t, c.Next())
	require.Len(t, c.Messages(), 1, "unexecuted tool group must not be published")
	require.Len(t, c.CallTools(), 2)
	require.Equal(t, 1, requests, "CallTools must not start network I/O")
	require.Len(t, c.Messages(), 4)
	require.Empty(t, c.CallTools())
	require.False(t, c.Next())
	require.ErrorIs(t, c.Err(), failure)
	require.Equal(t, 2, calls)
	require.Len(t, c.Messages(), 4, "failure retains committed tool results")
	snapshot := c.Messages()
	snapshot[1].ProviderData["opaque"][0] = 'x'
	snapshot[2].ToolCalls[0].Function.Arguments[0] = 'x'
	require.Equal(t, byte('{'), c.Messages()[1].ProviderData["opaque"][0])
	require.Equal(t, byte('{'), c.Messages()[2].ToolCalls[0].Function.Arguments[0])
}

func TestConversationCancellationCompletesToolGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls, requests := 0, 0
	c := NewConversation(ctx, proto.Request{ResultCaller: func(proto.ToolCallRequest) (proto.ToolResult, error) {
		calls++
		cancel()
		return proto.ToolResult{Text: "write completed"}, nil
	}}, func(context.Context, proto.Request) Round {
		requests++
		return Failed(nil, []proto.Message{toolAssistant()})
	})
	require.False(t, c.Next())
	statuses := c.CallTools()
	require.Len(t, statuses, 2)
	require.NoError(t, statuses[0].Err)
	require.ErrorIs(t, statuses[1].Err, context.Canceled)
	require.Equal(t, 1, calls)
	messages := c.Messages()
	require.Len(t, messages, 3)
	require.Equal(t, "write completed", messages[1].Content)
	require.True(t, messages[2].ToolCalls[0].IsError)
	require.False(t, c.Next())
	require.Empty(t, c.CallTools())
	require.Equal(t, 1, requests)
}

func TestTranscriptOwnsMediaBytes(t *testing.T) {
	input := []proto.Message{{Media: []proto.MediaAttachment{{Kind: "image", Data: []byte{1, 2}}}}}
	var transcript Transcript
	transcript.Replace(input)
	input[0].Media[0].Data[0] = 9
	snapshot := transcript.Snapshot()
	require.Equal(t, byte(1), snapshot[0].Media[0].Data[0])
	snapshot[0].Media[0].Data[0] = 8
	require.Equal(t, byte(1), transcript.Snapshot()[0].Media[0].Data[0])
}
