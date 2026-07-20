package prompts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinPrompts(t *testing.T) {
	defs := Builtin()
	byName := make(map[string]Definition, len(defs))
	for _, def := range defs {
		require.NotEmpty(t, def.Name)
		require.NotEmpty(t, def.Default)
		byName[def.Name] = def
	}

	for _, name := range []string{
		KeyIdentity,
		KeyToolSelection,
		KeyPlan,
		KeyShellClassifier,
		KeyMinimal,
		KeyFormatMarkdown,
		KeyFormatJSON,
		KeySafeWorkspaceTemplate,
		KeyApprovedPlanTemplate,
	} {
		require.Contains(t, byName, name)
	}

	require.True(t, byName[KeyIdentity].Configurable)
	require.True(t, byName[KeyShellClassifier].Configurable)
	require.False(t, byName[KeyMinimal].Configurable)
	require.Equal(t, Plan, byName[KeyPlan].Default)
	require.Equal(t, ShellClassifier, byName[KeyShellClassifier].Default)

	require.Contains(t, Identity, "execute it directly and rely on mods' review step")
	require.Contains(t, Identity, "state it briefly and proceed")
	require.Contains(t, Identity, "fs_replace")
	require.Contains(t, ToolSelection, "call the appropriate tool")
	require.Contains(t, ToolSelection, "fs_replace")
	require.Contains(t, ToolSelection, "PowerShell 5.1")
	require.Contains(t, ToolSelection, "Do not retry blindly")
}

func TestIdentityHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Identity, "call `mods_help`")
	require.Contains(t, Identity, "exact active config path")
	require.Contains(t, Identity, "next mods invocation")
	require.Contains(t, Identity, "`reasoning-effort-off`")
	require.Contains(t, Identity, "Responses API with `store: false`")
	require.Contains(t, Identity, "continue to use Chat Completions")
}

func TestDefaultRuntimePromptsStayCompact(t *testing.T) {
	require.LessOrEqual(t, len(Identity)+len(ToolSelection), 5*1024,
		"default identity and tool-selection prompts must stay within 5 KiB")
}

func TestDefaultPlanPromptStaysCompactAndComplete(t *testing.T) {
	require.GreaterOrEqual(t, len(Plan), 1600)
	require.LessOrEqual(t, len(Plan), 2000)
	for _, required := range []string{
		"Planning is read-only",
		"about 3 to 5",
		"## Plan",
		"## Proposal 1:",
		"**Approach**",
		"**Steps**",
		"**Files**",
		"**Commands**",
		"**Risks**",
		"write None",
	} {
		require.Contains(t, Plan, required)
	}
}
