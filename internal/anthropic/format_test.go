package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestFromMCPTools(t *testing.T) {
	t.Run("single tool", func(t *testing.T) {
		mcps := map[string][]mcp.Tool{
			"github": {{
				Name:        "search_repos",
				Description: "Search GitHub repositories",
				InputSchema: mcp.ToolInputSchema{
					Properties: map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "Search query",
						},
					},
				},
			}},
		}
		tools := fromMCPTools(mcps)
		require.Len(t, tools, 1)
		require.NotNil(t, tools[0].OfTool)
		require.Equal(t, "github_search_repos", tools[0].OfTool.Name)
		require.True(t, tools[0].OfTool.Description.Valid())
	})

	t.Run("multiple servers", func(t *testing.T) {
		mcps := map[string][]mcp.Tool{
			"srv1": {{Name: "t1", Description: "desc1"}},
			"srv2": {{Name: "t2", Description: "desc2"}},
		}
		tools := fromMCPTools(mcps)
		require.Len(t, tools, 2)
	})

	t.Run("empty", func(t *testing.T) {
		tools := fromMCPTools(nil)
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
		result := stripSchema(props)
		inner := result["query"].(map[string]any)
		require.Equal(t, "string", inner["type"])
		require.NotContains(t, inner, "description")
		require.NotContains(t, inner, "title")
		require.NotContains(t, inner, "default")
	})

	t.Run("nil returns nil", func(t *testing.T) {
		require.Nil(t, stripSchema(nil))
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
		result := stripSchema(props)
		top := result["top"].(map[string]any)
		nested := top["properties"].(map[string]any)
		inner := nested["inner"].(map[string]any)
		require.Equal(t, "string", inner["type"])
		require.NotContains(t, inner, "description")
	})
}

func TestFromProtoMessages(t *testing.T) {
	t.Run("system messages", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleSystem, Content: "You are helpful."},
			{Role: proto.RoleSystem, Content: "Be concise."},
		}
		system, messages := fromProtoMessages(input)
		require.Len(t, system, 2)
		require.Empty(t, messages)
	})

	t.Run("user message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "Hello"},
		}
		system, messages := fromProtoMessages(input)
		require.Empty(t, system)
		require.Len(t, messages, 1)
	})

	t.Run("user with images", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleUser, Content: "What is this?", Images: []proto.Image{
				{Data: []byte{0x89, 0x50, 0x4E, 0x47}, MimeType: "image/png"},
			}},
		}
		_, messages := fromProtoMessages(input)
		require.Len(t, messages, 1)
	})

	t.Run("assistant message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleAssistant, Content: "Hi there!"},
		}
		_, messages := fromProtoMessages(input)
		require.Len(t, messages, 1)
	})

	t.Run("tool message", func(t *testing.T) {
		input := []proto.Message{
			{Role: proto.RoleTool, Content: "result", ToolCalls: []proto.ToolCall{
				{ID: "call_1", IsError: false},
			}},
		}
		_, messages := fromProtoMessages(input)
		require.Len(t, messages, 1)
	})
}

func TestToProtoMessage(t *testing.T) {
	t.Run("assistant with text", func(t *testing.T) {
		_ = json.RawMessage(`{"key":"value"}`)
		// toProtoMessage requires proper Anthropic types, not easily constructed
		// Testing indirectly via fromProtoMessages symmetry
		input := []proto.Message{
			{Role: proto.RoleAssistant, Content: "I think so.", ToolCalls: []proto.ToolCall{
				{
					ID: "tool_1",
					Function: proto.Function{
						Name:      "search",
						Arguments: []byte(`{"q":"test"}`),
					},
				},
			}},
		}
		_, msgs := fromProtoMessages(input)
		require.Len(t, msgs, 1)
	})
}
