package prompts

import (
	"runtime"
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
	require.False(t, byName[KeyMinimal].Configurable)
	require.False(t, byName[KeyLanguagePolicy].Configurable)
	require.Equal(t, Workflow, byName[KeyWorkflow].Default)
	require.Equal(t, LanguagePolicy, byName[KeyLanguagePolicy].Default)

	require.Contains(t, Identity, "execute it directly and rely on mods' review step")
	require.Contains(t, Identity, "state it briefly and proceed")
	require.Contains(t, ToolSelection, "fs_replace")
	require.Contains(t, ToolSelection, "process_run")
	require.Contains(t, ToolSelection, "runtime_info")
	require.Contains(t, ToolSelection, "reported PowerShell host")
	require.Contains(t, ToolSelection, "git ls-files -z | xargs -0")
	require.Contains(t, ToolSelection, "without sh -c or bash -c")
	require.Contains(t, ToolSelectionShellPOSIXFallback, "without sh -c or bash -c")
	require.Contains(t, Identity, "Recognized reads run without review")
	require.NotContains(t, ToolSelection, "Recognized read-only commands run without review")
	require.Contains(t, ToolSelectionShellWindows, "prefer native cmdlets")
	require.Contains(t, ToolSelection, "Do not retry blindly")
	require.NotContains(t, ToolSelection, ScriptPolicy)
}

// TestInteractiveCredentialsStayOutOfIdentity keeps sudo/Git credential policy
// in its conditional fragment: without a terminal those flows cannot prompt.
func TestInteractiveCredentialsStayOutOfIdentity(t *testing.T) {
	for _, secretFlow := range []string{"sudo", "Git HTTP(S)"} {
		require.NotContains(t, Identity, secretFlow, "identity must not describe %q", secretFlow)
		require.Contains(t, InteractiveCredentials, secretFlow)
	}
	byName := make(map[string]Definition)
	for _, def := range Builtin() {
		byName[def.Name] = def
	}
	require.Contains(t, byName, KeyInteractiveCredentials)
	require.False(t, byName[KeyInteractiveCredentials].Configurable)
	require.Equal(t, InteractiveCredentialsForOS(runtime.GOOS), byName[KeyInteractiveCredentials].Default)
}

func TestCredentialPolicyMatchesPlatform(t *testing.T) {
	windows := InteractiveCredentialsForOS("windows")
	require.Contains(t, windows, "system UAC")
	require.Contains(t, windows, "Never collect administrator passwords")
	require.Contains(t, windows, "Git HTTP(S)")
	require.NotContains(t, windows, "sudo")
	require.NotContains(t, windows, "askpass")
	for _, goos := range []string{"linux", "darwin"} {
		require.Contains(t, InteractiveCredentialsForOS(goos), "mods supplies secure askpass")
	}
}

// TestIdentityStaysRuntimeSafetyOnly guards the layer split: identity is the
// highest-priority runtime-safety layer, so workflow policy (planning,
// diagnosis, verification, skills, self-help) must not live there.
func TestIdentityStaysRuntimeSafetyOnly(t *testing.T) {
	for _, workflowMarker := range []string{
		"todo_write",
		"Pin the symptom",
		"mods_help",
		"Editing and verification",
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
	require.Contains(t, Identity, "clear target and outcome")
	require.Contains(t, Identity, "ask before inspecting or changing an assumed target")
	require.NotContains(t, Identity, "not explicit modifications")
}

func TestWorkflowHasProportionateExecutionPolicy(t *testing.T) {
	for _, want := range []string{
		"not explicit modifications",
		"matching and quoting",
		"guessing permissions",
		"no concrete uncertainty remains, finish",
		"short final report",
	} {
		require.Contains(t, Workflow, want)
	}
}

func TestWorkflowHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Workflow, "call `mods_help`")
	require.Contains(t, Workflow, "instead of guessing")
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
	require.Contains(t, Workflow, "When `todo_write` is available")
	// A plan is opt-in for genuinely multi-stage work, never a default.
	require.Contains(t, Workflow, "at least three")
	require.Contains(t, Workflow, "dependent steps")
	require.Contains(t, Workflow, "do not plan a single question")
	require.Contains(t, Workflow, "single-file change")
	require.Contains(t, Workflow, "Never mark unverified work completed")
	// Step mechanics belong to the todo_write schema, not the always-on prompt.
	require.NotContains(t, Workflow, "full list of steps")
	require.NotContains(t, Workflow, "none when done")
}

// TestReviewPolicyLivesOnlyInIdentity keeps the review/permission policy in one
// place. Tool-selection guidance must not restate it, and identity must not
// carry ad-hoc shell prohibitions such as the old rm -rf rule.
func TestReviewPolicyLivesOnlyInIdentity(t *testing.T) {
	require.Contains(t, Identity, "rely on mods' review step")
	require.Contains(t, Identity, "Recognized reads run without review")
	require.NotContains(t, ToolSelection, "review step")
	require.NotContains(t, ToolSelection, "Use only tools available")
	require.NotContains(t, ToolSelection, "User denial")
	require.NotContains(t, Identity, "rm -rf")
	require.NotContains(t, Workflow, "rm -rf")
}

// TestAlwaysOnPromptsOmitCodeCountingProcedures keeps ordinary code-counting
// tasks from adding specialized procedures to every request.
func TestAlwaysOnPromptsOmitCodeCountingProcedures(t *testing.T) {
	for name, text := range map[string]string{KeyIdentity: Identity, KeyWorkflow: Workflow} {
		require.NotContains(t, text, "comment-excluded", name)
		require.NotContains(t, text, "code-size", name)
		require.NotContains(t, text, "language-aware", name)
	}
}

func TestWorkflowHasSymptomDiagnosisPolicy(t *testing.T) {
	require.Contains(t, Workflow, "Pin the symptom before inspecting")
	require.Contains(t, Workflow, "ask with `request_user_input` first")
	require.Contains(t, Workflow, "after ten without one")
}

func TestFixedPromptFragmentsStayCompact(t *testing.T) {
	// This local budget covers only fixed prompt fragments. App-level request
	// budgets measure rendered system sections and registered tool definitions.
	require.LessOrEqual(t,
		len(Identity)+len(Workflow)+len(LanguagePolicy)+len(ToolSelection)+len(InteractiveCredentials),
		5376, "default runtime prompts must stay within 5.25 KiB")
}

func TestIdentityHandlesUnavailableCapabilities(t *testing.T) {
	require.Contains(t, Identity, "Only tools supplied in this request")
	require.Contains(t, Identity, "When `request_user_input` is available")
	require.Contains(t, Identity, "Otherwise ask one concise text question")
	require.Contains(t, Identity, "ask for secrets in text")
	require.Contains(t, Workflow, "call `mods_help`")
	require.Contains(t, Workflow, "Without that tool, use the supplied self-help reference")
	require.Contains(t, Workflow, "version-matched help is unavailable")
}

func TestJSONFormatKeepsExplanationsInsideJSON(t *testing.T) {
	require.Contains(t, JSONFormat, "No Markdown fences or text outside JSON")
	require.Contains(t, JSONFormat, "summary, or report inside JSON fields")
	require.NotContains(t, JSONFormat, "unless")
}
