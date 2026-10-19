package proto

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMeasurePromptSizeIncludesRenderedSystemAndFullSchemas(t *testing.T) {
	identity := Message{Role: RoleSystem, Content: "Runtime safety."}
	identity.SetSystemSection(SystemSectionRuntimeIdentity)
	project := Message{Role: RoleSystem, Content: "项目约定"}
	project.SetSystemSection(SystemSectionProjectInstructions)
	messages := []Message{project, identity, {Role: RoleUser, Content: strings.Repeat("user data", 1000)}}
	tools := []ToolSpec{{Name: "inspect", Description: "Inspect a file.", InputSchema: map[string]any{
		"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string", "description": "Path to inspect."}},
	}}}
	size, err := MeasurePromptSize(messages, tools)
	require.NoError(t, err)
	require.Equal(t, len(RenderStructuredSystemPrompt(messages)), size.SystemBytes)
	require.Greater(t, size.SystemBytes, len(identity.Content)+len(project.Content), "rendered hierarchy must count")
	require.Len(t, size.Sections, 2)
	require.Equal(t, len(project.Content), size.Sections[1].Bytes, "measure UTF-8 bytes, not characters")
	definitions, err := json.Marshal(tools)
	require.NoError(t, err)
	require.Equal(t, len(definitions), size.ToolDefinitionsBytes)
	require.Equal(t, size.SystemBytes+len(definitions), size.TotalBytes())

	// Instructions hidden in schema property descriptions must consume budget,
	// even when the top-level tool description stays unchanged.
	tools[0].InputSchema["properties"].(map[string]any)["path"].(map[string]any)["description"] = strings.Repeat("schema guidance ", 1000)
	larger, err := MeasurePromptSize(messages, tools)
	require.NoError(t, err)
	require.Equal(t, size.ToolDescriptionBytes, larger.ToolDescriptionBytes)
	require.Greater(t, larger.ToolSchemaBytes, size.ToolSchemaBytes+10000)
	require.Greater(t, larger.TotalBytes(), size.TotalBytes()+10000)
}

func TestMeasurePromptSizeWithoutToolsOrStructuredSystem(t *testing.T) {
	size, err := MeasurePromptSize([]Message{{Role: RoleSystem, Content: "classifier"}, {Role: RoleUser, Content: "command"}}, nil)
	require.NoError(t, err)
	require.Equal(t, len("classifier"), size.TotalBytes())
	require.Zero(t, size.ToolDefinitionsBytes, "absent tools must not count JSON null")
	require.Empty(t, size.Tools)
	_, err = MeasurePromptSize(nil, []ToolSpec{{Name: "invalid", InputSchema: map[string]any{"invalid": func() {}}}})
	require.ErrorContains(t, err, `tool "invalid" schema`)
}
