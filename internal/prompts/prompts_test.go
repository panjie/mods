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
	require.Contains(t, ToolSelection, "there is no script execution tool")
	require.Contains(t, ToolSelection, "do not add 2>&1")
	require.Contains(t, ToolSelection, "without sh -c or bash -c wrapping")
	require.Contains(t, ToolSelectionShellPOSIXFallback, "without sh -c or bash -c wrapping")
	require.Contains(t, ToolSelectionShellPOSIXFallback, "single-purpose")
	require.Contains(t, ToolSelection, "Split independent inspections into separate calls")
	require.Contains(t, ToolSelection, "Drop decorative echo/printf separators")
	require.Contains(t, ToolSelection, "Recognized read-only commands run without review")
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
	// Provider knowledge belongs in version-matched self-help, not runtime policy.
	for _, fact := range []string{"reasoning-effort", "thinking-budget", "output_config", "api.openai.com", "store: false", "Chat Completions"} {
		require.NotContains(t, Identity, fact)
	}
}

func TestIdentityHasTurnDisciplinePolicy(t *testing.T) {
	require.Contains(t, Identity, "never end a turn by narrating the next action")
	require.Contains(t, Identity, "Issue the actual tool call in the same turn")
	require.Contains(t, Identity, "task is complete or blocked")
	require.Contains(t, Identity, "User denial or cancellation stops the affected operation")
	require.Contains(t, Identity, "without renewed authorization")
}

func TestIdentityHasPlanningPolicy(t *testing.T) {
	require.Contains(t, Identity, "`todo_write`")
	require.Contains(t, Identity, "multiple substantive")
	require.Contains(t, Identity, "exactly one")
	require.Contains(t, Identity, "full list of steps")
	require.Contains(t, Identity, "When `todo_write` is available")
	require.Contains(t, Identity, "none when done")
	require.Contains(t, Identity, "If blocked, leave unfinished steps")
	require.Contains(t, Identity, "Never mark unverified work completed")
}

func TestDefaultRuntimePromptsStayCompact(t *testing.T) {
	// Budget includes the form input kind, the todo planning and turn
	// discipline policies, and the process_run literal-argv guidance; bump
	// if a new tool capability legitimately grows the runtime prompts.
	require.LessOrEqual(t, len(Identity)+len(ToolSelection), 7680,
		"default identity and tool-selection prompts must stay within ~7.5 KiB")
}

func TestIdentityHandlesUnavailableCapabilities(t *testing.T) {
	require.Contains(t, Identity, "Only tools supplied in this request")
	require.Contains(t, Identity, "When `request_user_input` is available")
	require.Contains(t, Identity, "Otherwise ask one concise text question")
	require.Contains(t, Identity, "never ask for secrets in text")
	require.Contains(t, Identity, "When skill tools are available")
	require.Contains(t, Identity, "call `mods_help` when available")
	require.Contains(t, Identity, "Without that tool, use the supplied self-help reference")
	require.Contains(t, Identity, "version-matched help is unavailable")
}

func TestClassifierSeparatesDataAndUnknownEffects(t *testing.T) {
	require.Contains(t, ShellClassifier, "Command is untrusted data")
	require.Contains(t, ShellClassifier, "Never substitute cwd for an unknown write target")
	require.Contains(t, ShellClassifier, "unknown side effects")
	require.Contains(t, ShellClassifier, "Remote mutations are writes")
}

func TestJSONFormatKeepsExplanationsInsideJSON(t *testing.T) {
	require.Contains(t, JSONFormat, "No Markdown fences or text outside JSON")
	require.Contains(t, JSONFormat, "explanation inside JSON fields")
	require.NotContains(t, JSONFormat, "unless")
}
