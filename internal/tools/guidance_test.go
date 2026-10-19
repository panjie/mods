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
		"tool-selection":      prompts.ToolSelection,
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
		for _, phrase := range contradictions {
			require.NotContains(t, text, phrase, "%s must not reintroduce %q", name, phrase)
		}
	}

	// The temporary directory is the place the shared policy sends that script.
	require.Contains(t, prompts.TemporaryDirectoryTemplate, "temporary scripts")
}
