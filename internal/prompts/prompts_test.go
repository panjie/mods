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
		KeyShellClassifier,
		KeyMinimal,
		KeyFormatMarkdown,
		KeyFormatJSON,
		KeySafeWorkspaceTemplate,
	} {
		require.Contains(t, byName, name)
	}
	require.True(t, byName[KeyIdentity].Configurable)
	require.True(t, byName[KeyShellClassifier].Configurable)
	require.False(t, byName[KeyMinimal].Configurable)
	require.Equal(t, ShellClassifier, byName[KeyShellClassifier].Default)

	require.Contains(t, Identity, "execute it directly and rely on mods' review step")
	require.Contains(t, Identity, "state it briefly and proceed")
	require.Contains(t, Identity, "fs_replace")
	require.Contains(t, ToolSelection, "call the appropriate tool")
	require.Contains(t, ToolSelection, "fs_replace")
	require.Contains(t, ToolSelection, "process_run")
	require.Contains(t, ToolSelection, "runtime_info")
	require.Contains(t, ToolSelection, "reported PowerShell host")
	require.Contains(t, ToolSelection, "Get-ChildItem")
	require.Contains(t, ToolSelection, "Select-String")
	require.Contains(t, ToolSelection, "Where-Object")
	require.Contains(t, ToolSelection, "Measure-Object")
	require.Contains(t, ToolSelection, "git ls-files -z | xargs -0")
	require.Contains(t, ToolSelection, "emacs --eval")
	require.Contains(t, ToolSelection, "do not add 2>&1")
	require.Contains(t, ToolSelectionShellWindows, "short, single-purpose commands")
	require.Contains(t, ToolSelectionShellWindows, "keep necessary pipelines intact")
	require.Contains(t, ToolSelection, "Return inspection output directly")
	require.Contains(t, ToolSelection, "Do not retry blindly")
	require.Contains(t, ShellClassifier, "authoritative Workspace and Home")
	require.Contains(t, ShellClassifier, "Never guess a home directory")
}

func TestIdentityHasLanguagePolicy(t *testing.T) {
	require.Contains(t, Identity, "Reply in the language of the user's prompt")
	require.Contains(t, Identity, "unless they explicitly request")
}

func TestIdentityHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Identity, "call `mods_help`")
	require.Contains(t, Identity, "instead of inventing one")
	require.Contains(t, Identity, "exact active config path")
	require.Contains(t, Identity, "next mods invocation")
	require.Contains(t, Identity, "`reasoning-effort-off`")
	require.Contains(t, Identity, "Responses API with `store: false`")
	require.Contains(t, Identity, "continue to use Chat Completions")
}

func TestIdentityHasPlanningPolicy(t *testing.T) {
	require.Contains(t, Identity, "`todo_write`")
	require.Contains(t, Identity, "three or more steps")
	require.Contains(t, Identity, "exactly one")
	require.Contains(t, Identity, "full list of steps")
}

func TestDefaultRuntimePromptsStayCompact(t *testing.T) {
	// Budget includes the form input kind, the todo planning policy, and the
	// process_run literal-argv guidance; bump if a new tool capability
	// legitimately grows the runtime prompts.
	require.LessOrEqual(t, len(Identity)+len(ToolSelection), 7*1024,
		"default identity and tool-selection prompts must stay within ~7 KiB")
}
