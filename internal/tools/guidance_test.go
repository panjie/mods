package tools

import (
	"testing"

	"github.com/panjie/mods/internal/prompts"
	"github.com/stretchr/testify/require"
)

// TestScriptGuidanceIsSharedAndConsistent guards the script policy. Every
// always-on text that recommends a script must use the shared wording, and none
// may reintroduce the contradictory "never use temporary scripts" or "split
// multi-step work" advice that previously coexisted with it.
func TestScriptGuidanceIsSharedAndConsistent(t *testing.T) {
	texts := map[string]string{
		"process_run":         ProcessRunDescription,
		"shell_run (posix)":   PosixShellRunDescription,
		"shell_run (windows)": WindowsShellRunDescription,
		"powershell_run":      PowerShellRunDescription,
	}
	contradictions := []string{
		"Do not hide code",
		"split multi-step work",
		"no script execution tool",
		"do not combine independent operations",
		"single-purpose",
	}
	for name, text := range texts {
		require.Contains(t, text, prompts.ScriptPolicy, "%s must use the shared script policy", name)
		require.Contains(t, text, "Select/read/filter/aggregate with read-only pipelines")
		require.Contains(t, text, "Compute requested metrics in tools, not from text or file sizes")
		require.Contains(t, text, "Group related reads, not stateful scripts")
		require.Contains(t, text, "suppressed errors and query files")
		require.Contains(t, text, "Use a script only if pipelines cannot suffice")
		require.Contains(t, text, "Avoid existence checks, suppressed errors")
		require.NotContains(t, text, "For multi-step or aggregation work, write")
		for _, phrase := range contradictions {
			require.NotContains(t, text, phrase, "%s must not reintroduce %q", name, phrase)
		}
	}

	// The temporary directory is the place the shared policy sends that script.
	require.Contains(t, prompts.TemporaryDirectoryTemplate, "temporary scripts")
	// The system prompt no longer restates script mechanics; the descriptions
	// are the single place the model reads them.
	require.NotContains(t, prompts.ToolSelection, prompts.ScriptPolicy)
}

// TestToolDescriptionsCarryMechanicsThatLeftTheSystemPrompt guards the dedupe:
// detail removed from tool-selection guidance must still reach the model through
// the description of the tool it applies to.
func TestToolDescriptionsCarryMechanicsThatLeftTheSystemPrompt(t *testing.T) {
	for name, desc := range map[string]string{
		"powershell_run":      PowerShellRunDescription,
		"shell_run (windows)": WindowsShellRunDescription,
	} {
		for _, cmdlet := range []string{"Get-ChildItem", "Select-String", "Where-Object", "Measure-Object"} {
			require.Contains(t, desc, cmdlet, "%s must document %s", name, cmdlet)
		}
	}
	require.Contains(t, PosixShellRunDescription, prompts.POSIXIntentGuidance)
	require.Contains(t, PowerShellRunDescription, prompts.PowerShellIntentGuidance)
}
