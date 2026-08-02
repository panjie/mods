package ollama

import (
	"encoding/json"
	"testing"

	api "github.com/panjie/mods/internal/ollamaapi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestFromToolSpecs(t *testing.T) {
	t.Run("single tool", func(t *testing.T) {
		specs := []proto.ToolSpec{{
			Name:        "read_file",
			Description: "Read a file",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{
						"type":        "string",
						"description": "File path",
					},
				},
			},
		}}
		tools := fromToolSpecs(specs)
		require.Len(t, tools, 1)
		require.Equal(t, "function", tools[0].Type)
		require.Equal(t, "read_file", tools[0].Function.Name)
		require.Equal(t, "Read a file", tools[0].Function.Description)
	})

	t.Run("empty", func(t *testing.T) {
		tools := fromToolSpecs(nil)
		require.Empty(t, tools)
	})
}

func TestFromProtoMessage(t *testing.T) {
	t.Run("basic message", func(t *testing.T) {
		input := proto.Message{
			Role:    proto.RoleUser,
			Content: "Hello",
		}
		msg := fromProtoMessage(input)
		require.Equal(t, "user", msg.Role)
		require.Equal(t, "Hello", msg.Content)
	})

	t.Run("with images", func(t *testing.T) {
		input := proto.Message{
			Role:    proto.RoleUser,
			Content: "What is this?",
			Images: []proto.Image{
				{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MimeType: "image/png"},
			},
		}
		msg := fromProtoMessage(input)
		require.Len(t, msg.Images, 1)
	})

	t.Run("assistant with tool calls", func(t *testing.T) {
		input := proto.Message{
			Role: proto.RoleAssistant,
			ToolCalls: []proto.ToolCall{
				{
					ID: "0",
					Function: proto.Function{
						Name:      "read_file",
						Arguments: []byte(`{"path":"/tmp/test.txt"}`),
					},
				},
			},
		}
		msg := fromProtoMessage(input)
		require.Len(t, msg.ToolCalls, 1)
		require.Equal(t, "read_file", msg.ToolCalls[0].Function.Name)
	})

	t.Run("tool result", func(t *testing.T) {
		input := proto.Message{
			Role:    proto.RoleTool,
			Content: "file contents",
			ToolCalls: []proto.ToolCall{{
				ID: "0",
				Function: proto.Function{
					Name:      "read_file",
					Arguments: []byte(`{"path":"/tmp/test.txt"}`),
				},
			}},
		}
		msg := fromProtoMessage(input)
		require.Equal(t, "tool", msg.Role)
		require.Equal(t, "read_file", msg.ToolName)
		require.Empty(t, msg.ToolCalls)

		wire, err := json.Marshal(msg)
		require.NoError(t, err)
		require.JSONEq(t, `{"role":"tool","content":"file contents","tool_name":"read_file"}`, string(wire))
	})

	t.Run("parallel tool results preserve names", func(t *testing.T) {
		messages := fromProtoMessages([]proto.Message{
			{
				Role:    proto.RoleTool,
				Content: "22C",
				ToolCalls: []proto.ToolCall{{
					Function: proto.Function{Name: "get_temperature"},
				}},
			},
			{
				Role:    proto.RoleTool,
				Content: "sunny",
				ToolCalls: []proto.ToolCall{{
					Function: proto.Function{Name: "get_conditions"},
				}},
			},
		})
		require.Len(t, messages, 2)
		require.Equal(t, "get_temperature", messages[0].ToolName)
		require.Equal(t, "get_conditions", messages[1].ToolName)
		require.Empty(t, messages[0].ToolCalls)
		require.Empty(t, messages[1].ToolCalls)
	})
}

func TestFromProtoMessagesMergesStructuredSystemPrompt(t *testing.T) {
	identity := proto.Message{Role: proto.RoleSystem, Content: "identity"}
	identity.SetSystemSection(proto.SystemSectionRuntimeIdentity)
	format := proto.Message{Role: proto.RoleSystem, Content: "format"}
	format.SetSystemSection(proto.SystemSectionOutputFormat)

	got := fromProtoMessages([]proto.Message{
		format,
		{Role: proto.RoleUser, Content: "hello"},
		identity,
	})
	require.Len(t, got, 2)
	require.Equal(t, proto.RoleSystem, got[0].Role)
	require.Contains(t, got[0].Content, "identity")
	require.Contains(t, got[0].Content, "format")
	require.Equal(t, proto.RoleUser, got[1].Role)
}

func TestToProtoMessage(t *testing.T) {
	t.Run("basic message", func(t *testing.T) {
		input := api.Message{
			Role:    "assistant",
			Content: "I am an AI.",
		}
		msg := toProtoMessage(input)
		require.Equal(t, "assistant", msg.Role)
		require.Equal(t, "I am an AI.", msg.Content)
	})

	t.Run("with tool calls", func(t *testing.T) {
		input := api.Message{
			Role:    "assistant",
			Content: "result",
			ToolCalls: []api.ToolCall{
				{
					Function: api.ToolCallFunction{
						Index: 0,
						Name:  "search",
					},
				},
			},
		}
		msg := toProtoMessage(input)
		require.Len(t, msg.ToolCalls, 1)
		require.Equal(t, "search", msg.ToolCalls[0].Function.Name)
		require.Equal(t, "0", msg.ToolCalls[0].ID)
	})
}

func TestFromProtoMessages(t *testing.T) {
	t.Run("multiple messages", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleSystem, Content: "System"},
			{Role: proto.RoleUser, Content: "User msg"},
			{Role: proto.RoleAssistant, Content: "Assistant msg"},
		}
		msgs := fromProtoMessages(input)
		require.Len(t, msgs, 3)
		require.Equal(t, "system", msgs[0].Role)
		require.Equal(t, "user", msgs[1].Role)
		require.Equal(t, "assistant", msgs[2].Role)
	})
}
