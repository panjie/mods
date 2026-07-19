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
	plan := Message{Role: RoleSystem, Content: "PLAN_TOKEN"}
	plan.SetSystemSection(SystemSectionRuntimePlan)
	role2 := Message{Role: RoleSystem, Content: "ROLE_TWO_TOKEN"}
	role2.SetSystemSection(SystemSectionUserRole)

	got := NormalizeSystemMessages([]Message{
		format, role1, project,
		{Role: RoleUser, Content: "hello"},
		tools, identity, plan, role2,
	})
	require.Len(t, got, 2)
	require.Equal(t, RoleSystem, got[0].Role)
	require.Equal(t, RoleUser, got[1].Role)
	require.Contains(t, got[0].Content, "runtime safety > execution capability > project instructions > user role > output format")
	for _, pair := range [][2]string{
		{"IDENTITY_TOKEN", "PLAN_TOKEN"},
		{"PLAN_TOKEN", "TOOLS_TOKEN"},
		{"TOOLS_TOKEN", "PROJECT_TOKEN"},
		{"PROJECT_TOKEN", "ROLE_ONE_TOKEN"},
		{"ROLE_ONE_TOKEN", "ROLE_TWO_TOKEN"},
		{"ROLE_TWO_TOKEN", "FORMAT_TOKEN"},
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
