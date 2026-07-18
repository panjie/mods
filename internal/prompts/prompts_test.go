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

	require.Contains(t, Identity, "execute it directly rather than asking for permission")
	require.Contains(t, Identity, "built-in review step that gates mutating changes")
	require.Contains(t, Identity, "state it briefly in one line, then proceed")
	require.Contains(t, Identity, "fs_replace")
	require.Contains(t, ToolSelection, "attempt it directly rather than asking for permission first")
	require.Contains(t, ToolSelection, "fs_replace")
	require.Contains(t, ToolSelection, "rg --files")
	require.Contains(t, ToolSelection, "powershell_run")
	require.Contains(t, ToolSelection, "Set-Location")
	require.Contains(t, ToolSelection, "go test ./...")
	require.Contains(t, ToolSelection, "Do not keep retrying blindly")
}

func TestIdentityHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Identity, "fs_read_file ~/.config/mods/mods.yml",
		"must instruct LLM how to read config")
	require.Contains(t, Identity, `Get-Content (Join-Path $env:USERPROFILE ".config\mods\mods.yml")`,
		"must give Windows config examples in PowerShell syntax")
	require.Contains(t, Identity, "Self-help policy",
		"must have a self-help section")
}
