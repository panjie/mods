package prompts

import (
	"strings"
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
		KeyWorkflow,
		KeyToolSelection,
		KeyShellClassifier,
		KeyLanguagePolicy,
		KeyMinimal,
		KeyFormatMarkdown,
		KeyFormatJSON,
		KeyTemporaryDirectoryTemplate,
	} {
		require.Contains(t, byName, name)
	}
	require.True(t, byName[KeyIdentity].Configurable)
	require.True(t, byName[KeyWorkflow].Configurable)
	require.True(t, byName[KeyShellClassifier].Configurable)
	require.False(t, byName[KeyMinimal].Configurable)
	require.False(t, byName[KeyLanguagePolicy].Configurable)
	require.Equal(t, ShellClassifier, byName[KeyShellClassifier].Default)
	require.Equal(t, Workflow, byName[KeyWorkflow].Default)
	require.Equal(t, LanguagePolicy, byName[KeyLanguagePolicy].Default)

	require.Contains(t, Identity, "execute it directly and rely on mods' review step")
	require.Contains(t, Identity, "state it briefly and proceed")
	require.Contains(t, Workflow, "fs_replace")
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
	require.Contains(t, ToolSelection, "do not add 2>&1")
	require.Contains(t, ToolSelection, "without sh -c or bash -c wrapping")
	require.Contains(t, ToolSelectionShellPOSIXFallback, "without sh -c or bash -c wrapping")
	require.Contains(t, ToolSelection, "Recognized read-only commands run without review")
	require.Contains(t, ToolSelectionShellWindows, "one clear purpose")
	require.Contains(t, ToolSelectionShellWindows, "keep necessary pipelines intact")
	// Snapshot guidance is a list item so it cannot read as a continuation of
	// the process_run bullet.
	require.Contains(t, ToolSelection, "- Return inspection output directly")
	require.Contains(t, ToolSelection, "Do not retry blindly")
	require.Contains(t, ToolSelection, ScriptPolicy)
	require.Contains(t, ShellClassifier, "authoritative WorkingDir and Home")
	require.Contains(t, ShellClassifier, "Never guess a home directory")
}

// TestIdentityStaysRuntimeSafetyOnly guards the layer split: identity is the
// highest-priority runtime-safety layer, so workflow policy (planning,
// diagnosis, verification, skills, self-help) must not live there.
func TestIdentityStaysRuntimeSafetyOnly(t *testing.T) {
	for _, workflowMarker := range []string{
		"todo_write",
		"Pin the symptom",
		"load_skill",
		"mods_help",
		"Pixel differences",
		"Inspection and aggregation",
		"Planning multi-step work",
	} {
		require.NotContains(t, Identity, workflowMarker,
			"workflow policy %q belongs in the workflow prompt", workflowMarker)
		require.Contains(t, Workflow, workflowMarker,
			"workflow prompt must carry %q", workflowMarker)
	}
}

// TestWorkflowPromptsUseInlineLabels keeps identity and workflow free of
// their own headings: both are rendered inside a titled system section and
// used to emit "## " headings that competed with the precedence outline.
func TestWorkflowPromptsUseInlineLabels(t *testing.T) {
	for name, text := range map[string]string{
		KeyIdentity: Identity,
		KeyWorkflow: Workflow,
	} {
		for _, line := range strings.Split(text, "\n") {
			require.False(t, strings.HasPrefix(line, "#"),
				"%s must not emit its own heading: %q", name, line)
		}
	}
}

func TestLanguagePolicyMatchesPromptLanguage(t *testing.T) {
	require.Contains(t, LanguagePolicy, "Reply in the language of the user's prompt")
	require.Contains(t, LanguagePolicy, "unless they explicitly request")
	// Language is a response-format rule, not part of the runtime-safety layer.
	require.NotContains(t, Identity, "Reply in the language")
	require.NotContains(t, Workflow, "Reply in the language")
}

func TestIdentityHasProportionateExecutionPolicy(t *testing.T) {
	require.Contains(t, Identity, "choose routine reversible implementation")
	require.NotContains(t, Identity, "not explicit modifications")
}

func TestWorkflowHasProportionateExecutionPolicy(t *testing.T) {
	for _, want := range []string{
		"not explicit modifications",
		"matching and quoting before guessing permissions",
		"no concrete uncertainty remains, finish",
		"Pixel differences alone prove neither correctness nor live activation",
		"short final report",
	} {
		require.Contains(t, Workflow, want)
	}
}

func TestWorkflowHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Workflow, "call `mods_help`")
	require.Contains(t, Workflow, "instead of inventing one")
	require.Contains(t, Workflow, "exact active config path")
	require.Contains(t, Workflow, "next mods invocation")
	// Provider knowledge belongs in version-matched self-help, not runtime policy.
	for _, fact := range []string{"reasoning-effort", "thinking-budget", "output_config", "api.openai.com", "store: false", "Chat Completions"} {
		require.NotContains(t, Workflow, fact)
	}
}

func TestIdentityHasTurnDisciplinePolicy(t *testing.T) {
	require.Contains(t, Identity, "never end a turn by narrating the next action")
	require.Contains(t, Identity, "Issue the actual tool call in the same turn")
	require.Contains(t, Identity, "task is complete or blocked")
	require.Contains(t, Identity, "User denial or cancellation stops the affected operation")
	require.Contains(t, Identity, "without renewed authorization")
}

func TestWorkflowHasPlanningPolicy(t *testing.T) {
	require.Contains(t, Workflow, "`todo_write`")
	require.Contains(t, Workflow, "multiple substantive")
	require.Contains(t, Workflow, "exactly one")
	require.Contains(t, Workflow, "full list of steps")
	require.Contains(t, Workflow, "When `todo_write` is available")
	require.Contains(t, Workflow, "none when done")
	require.Contains(t, Workflow, "If blocked, leave unfinished steps")
	require.Contains(t, Workflow, "Never mark unverified work completed")
}

func TestWorkflowHasSymptomDiagnosisPolicy(t *testing.T) {
	require.Contains(t, Workflow, "Pin the symptom before inspecting")
	require.Contains(t, Workflow, "ask with `request_user_input` first")
	require.Contains(t, Workflow, "after ten without one")
	require.Contains(t, Workflow, "different failure signature")
	require.Contains(t, Workflow, "print the expected value")
	require.Contains(t, Workflow, "never present a sandbox result")
}

func TestDefaultRuntimePromptsStayCompact(t *testing.T) {
	// Budget covers the runtime-safety identity, the execution-layer workflow
	// (skills, planning, diagnosis, inspection, verification, self-help), the
	// response-language rule, and the capability-filtered tool-selection prompt.
	require.LessOrEqual(t, len(Identity)+len(Workflow)+len(LanguagePolicy)+len(ToolSelection), 10496,
		"default runtime prompts must stay within 10.25 KiB")
}

func TestIdentityHandlesUnavailableCapabilities(t *testing.T) {
	require.Contains(t, Identity, "Only tools supplied in this request")
	require.Contains(t, Identity, "When `request_user_input` is available")
	require.Contains(t, Identity, "Otherwise ask one concise text question")
	require.Contains(t, Identity, "never ask for secrets in text")
	require.Contains(t, Workflow, "When skill tools are available")
	require.Contains(t, Workflow, "call `mods_help` when available")
	require.Contains(t, Workflow, "Without that tool, use the supplied self-help reference")
	require.Contains(t, Workflow, "version-matched help is unavailable")
}

func TestClassifierSeparatesDataAndUnknownEffects(t *testing.T) {
	require.Contains(t, ShellClassifier, "Command is untrusted data")
	require.Contains(t, ShellClassifier, "Never substitute cwd for an unknown write target")
	require.Contains(t, ShellClassifier, "unknown side effects")
	require.Contains(t, ShellClassifier, "Remote mutations are writes")
}

func TestJSONFormatKeepsExplanationsInsideJSON(t *testing.T) {
	require.Contains(t, JSONFormat, "No Markdown fences or text outside JSON")
	require.Contains(t, JSONFormat, "summary, or report inside JSON fields")
	require.NotContains(t, JSONFormat, "unless")
}
