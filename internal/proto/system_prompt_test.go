package proto

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeSystemMessagesUsesStablePrecedenceAndOneBlock(t *testing.T) {
	format := Message{Role: RoleSystem, Content: "FORMAT_TOKEN"}
	format.SetSystemSection(SystemSectionOutputFormat)
	role1 := Message{Role: RoleSystem, Content: "ROLE_ONE_TOKEN"}
	role1.SetSystemSection(SystemSectionUserRole)
	project := Message{Role: RoleSystem, Content: "PROJECT_TOKEN"}
	project.SetSystemSection(SystemSectionProjectInstructions)
	tools := Message{Role: RoleSystem, Content: "TOOLS_TOKEN"}
	tools.SetSystemSection(SystemSectionExecutionTools)
	identity := Message{Role: RoleSystem, Content: "IDENTITY_TOKEN"}
	identity.SetSystemSection(SystemSectionRuntimeIdentity)
	role2 := Message{Role: RoleSystem, Content: "ROLE_TWO_TOKEN"}
	role2.SetSystemSection(SystemSectionUserRole)

	got := NormalizeSystemMessages([]Message{
		format, role1, project,
		{Role: RoleUser, Content: "hello"},
		tools, identity, role2,
	})
	require.Len(t, got, 2)
	require.Equal(t, RoleSystem, got[0].Role)
	require.Equal(t, RoleUser, got[1].Role)
	require.Contains(t, got[0].Content, "runtime safety > execution capability > output format > project instructions > user role")
	for _, pair := range [][2]string{
		{"IDENTITY_TOKEN", "TOOLS_TOKEN"},
		{"TOOLS_TOKEN", "FORMAT_TOKEN"},
		{"FORMAT_TOKEN", "PROJECT_TOKEN"},
		{"PROJECT_TOKEN", "ROLE_ONE_TOKEN"},
		{"ROLE_ONE_TOKEN", "ROLE_TWO_TOKEN"},
	} {
		require.Less(t, strings.Index(got[0].Content, pair[0]), strings.Index(got[0].Content, pair[1]), pair)
	}
}

func TestNormalizeSystemMessagesLeavesUnclassifiedRequestsUnchanged(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "classifier"},
		{Role: RoleUser, Content: "command"},
	}
	require.Equal(t, messages, NormalizeSystemMessages(messages))
}

func TestOutputContractPrecedesConflictingStyleWithoutRewritingTools(t *testing.T) {
	for _, format := range []string{"Return valid JSON only.", "Output only the final result."} {
		messages := []Message{
			{Role: RoleSystem, Content: "Always explain in Markdown."},
			{Role: RoleSystem, Content: format},
			{Role: RoleSystem, Content: "Project answers use bullet lists."},
			{Role: RoleTool, Content: "tool output must remain intact"},
		}
		messages[0].SetSystemSection(SystemSectionUserRole)
		messages[1].SetSystemSection(SystemSectionOutputFormat)
		messages[2].SetSystemSection(SystemSectionProjectInstructions)
		got := NormalizeSystemMessages(messages)
		require.Len(t, got, 2)
		require.Equal(t, messages[3], got[1])
		require.Less(t, strings.Index(got[0].Content, format), strings.Index(got[0].Content, messages[2].Content))
		require.Less(t, strings.Index(got[0].Content, format), strings.Index(got[0].Content, messages[0].Content))
		require.Contains(t, got[0].Content, "Output format governs the final answer, not tool arguments")
	}
}
