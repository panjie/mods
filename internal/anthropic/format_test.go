package anthropic

import (
	"encoding/json"
	"testing"

	SDK "github.com/anthropics/anthropic-sdk-go"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestFromToolSpecs(t *testing.T) {
	t.Run("single tool", func(t *testing.T) {
		specs := []proto.ToolSpec{{
			Name:        "search_repos",
			Description: "Search GitHub repositories",
			InputSchema: map[string]any{
				"required": []any{"query"},
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
				},
			},
		}}
		tools := fromToolSpecs(specs)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		require.Equal(t, "search_repos", tools[0].OfTool.Name)
		require.True(t, tools[0].OfTool.Description.Valid())
		require.Equal(t, []string{"query"}, tools[0].OfTool.InputSchema.Required)
	})

	t.Run("multiple tools", func(t *testing.T) {
		specs := []proto.ToolSpec{
			{Name: "t1", Description: "desc1"},
			{Name: "t2", Description: "desc2"},
		}
		tools := fromToolSpecs(specs)
		require.Len(t, tools, 2)
	})

	t.Run("empty", func(t *testing.T) {
		tools := fromToolSpecs(nil)
		require.Empty(t, tools)
	})
}

func TestStripSchema(t *testing.T) {
	t.Run("removes description title default", func(t *testing.T) {
		props := map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "removed",
				"title":       "removed",
				"default":     "removed",
			},
		}
		result := proto.StripSchema(props)
		inner := result["query"].(map[string]any)
		require.Equal(t, "string", inner["type"])
		require.NotContains(t, inner, "description")
		require.NotContains(t, inner, "title")
		require.NotContains(t, inner, "default")
	})

	t.Run("nil returns nil", func(t *testing.T) {
		require.Nil(t, proto.StripSchema(nil))
	})

	t.Run("nested properties stripped", func(t *testing.T) {
		props := map[string]any{
			"top": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"inner": map[string]any{
						"type":        "string",
						"description": "removed",
					},
				},
			},
		}
		result := proto.StripSchema(props)
		top := result["top"].(map[string]any)
		nested := top["properties"].(map[string]any)
		inner := nested["inner"].(map[string]any)
		require.Equal(t, "string", inner["type"])
		require.NotContains(t, inner, "description")
	})
}

func TestFromProtoMessages(t *testing.T) {
	t.Run("structured system messages merge into one block", func(t *testing.T) {
		identity := proto.Message{Role: proto.RoleSystem, Content: "identity"}
		identity.SetSystemSection(proto.SystemSectionRuntimeIdentity)
		format := proto.Message{Role: proto.RoleSystem, Content: "format"}
		format.SetSystemSection(proto.SystemSectionOutputFormat)
		system, messages, err := fromProtoMessages([]proto.Message{
			format,
			{Role: proto.RoleUser, Content: "hello"},
			identity,
		})
		require.NoError(t, err)
		require.Len(t, system, 1)
		require.Contains(t, system[0].Text, "identity")
		require.Contains(t, system[0].Text, "format")
		require.Len(t, messages, 1)
	})

	t.Run("system messages", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleSystem, Content: "You are helpful."},
			{Role: proto.RoleSystem, Content: "Be concise."},
		}
		system, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Len(t, system, 2)
		require.Empty(t, messages)
	})

	t.Run("user message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "Hello"},
		}
		system, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Empty(t, system)
		require.Len(t, messages, 1)
	})

	t.Run("user with images", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "What is this?", Images: []proto.Image{
				{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MimeType: "image/png"},
			}},
		}
		_, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 1)
	})

	t.Run("assistant message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleAssistant, Content: "Hi there!"},
		}
		_, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 1)
	})

	t.Run("tool message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleTool, Content: "result one", ToolCalls: []proto.ToolCall{
				{ID: "call_1", IsError: false},
			}},
			{Role: proto.RoleTool, Content: "result two", ToolCalls: []proto.ToolCall{
				{ID: "call_2", IsError: true},
			}},
		}
		_, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Len(t, messages[0].Content, 2)
	})

	t.Run("provider content is replayed before visible fallback", func(t *testing.T) {
		state := json.RawMessage(`[
			{"type":"thinking","thinking":"summary","signature":"opaque-signature"},
			{"type":"redacted_thinking","data":"encrypted"},
			{"type":"tool_use","id":"tool_1","name":"search","input":{"q":"mods"}}
		]`)
		input := []proto.Message{{
			Role:    proto.RoleAssistant,
			Content: "must not replace provider state",
			ProviderData: map[string]json.RawMessage{
				messagesProviderDataKey: state,
			},
		}}
		_, messages, err := fromProtoMessages(input)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		require.Len(t, messages[0].Content, 3)
		require.Equal(t, "opaque-signature", messages[0].Content[0].OfThinking.Signature)
		require.Equal(t, "encrypted", messages[0].Content[1].OfRedactedThinking.Data)
		require.Equal(t, "tool_1", messages[0].Content[2].OfToolUse.ID)
	})

	t.Run("malformed provider content fails instead of rebuilding", func(t *testing.T) {
		input := []proto.Message{{
			Role: proto.RoleAssistant,
			ProviderData: map[string]json.RawMessage{
				messagesProviderDataKey: json.RawMessage(`{"not":"an array"}`),
			},
		}}
		_, _, err := fromProtoMessages(input)
		require.ErrorContains(t, err, "decode Anthropic provider content")
	})
}

func TestToProtoMessage(t *testing.T) {
	t.Run("assistant preserves ordered opaque blocks", func(t *testing.T) {
		var input SDK.Message
		err := json.Unmarshal([]byte(`{
			"id":"msg_1",
			"type":"message",
			"role":"assistant",
			"model":"claude-sonnet-4-6",
			"stop_reason":"tool_use",
			"stop_sequence":null,
			"usage":{"input_tokens":1,"output_tokens":2},
			"content":[
				{"type":"thinking","thinking":"summary","signature":"opaque-signature"},
				{"type":"redacted_thinking","data":"encrypted"},
				{"type":"text","text":"Checking."},
				{"type":"tool_use","id":"tool_1","name":"search","input":{"q":"test"}}
			]
		}`), &input)
		require.NoError(t, err)

		msg, err := toProtoMessage(input)
		require.NoError(t, err)
		require.Equal(t, proto.RoleAssistant, msg.Role)
		require.Equal(t, "Checking.", msg.Content)
		require.Len(t, msg.ToolCalls, 1)
		require.JSONEq(t, `{"q":"test"}`, string(msg.ToolCalls[0].Function.Arguments))
		require.NotEmpty(t, msg.ProviderData[messagesProviderDataKey])

		_, replayed, err := fromProtoMessages([]proto.Message{msg})
		require.NoError(t, err)
		require.Len(t, replayed, 1)
		require.Len(t, replayed[0].Content, 4)
		require.Equal(t, "opaque-signature", replayed[0].Content[0].OfThinking.Signature)
		require.Equal(t, "encrypted", replayed[0].Content[1].OfRedactedThinking.Data)
		require.Equal(t, "Checking.", replayed[0].Content[2].OfText.Text)
		require.Equal(t, "tool_1", replayed[0].Content[3].OfToolUse.ID)
	})
}
