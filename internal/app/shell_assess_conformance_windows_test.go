//go:build windows

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

// PowerShell half of the shell assessment conformance suite. It is
// Windows-only because the PowerShell analyzer needs a real PowerShell host to
// build its IR; the POSIX half in shell_assess_conformance_test.go runs
// everywhere.

func powershellConformanceCases() []conformanceCase {
	return []conformanceCase{
		{
			name: "Get-Content reads the named file", tool: "powershell_run",
			command: `Get-Content <EXT>\notes.txt`, want: approval.EffectRead,
			wantDirs: []string{`<EXT>\notes.txt`},
		},
		{
			name: "Set-Content writes target and parent", tool: "powershell_run",
			command: `Set-Content -Path <EXT>\out.txt -Value x`, want: approval.EffectWrite,
			wantDirs: []string{`<EXT>`, `<EXT>\out.txt`},
		},
		{
			name: "Remove-Item writes target and parent", tool: "powershell_run",
			command: `Remove-Item <EXT>\gone.txt`, want: approval.EffectWrite,
			wantDirs: []string{`<EXT>`, `<EXT>\gone.txt`},
		},
		{
			name: "quoted absolute read is an external fact", tool: "powershell_run",
			command: `Get-Content "C:\Windows\win.ini"`, want: approval.EffectRead,
			wantDirs: []string{`C:\Windows\win.ini`},
		},
		{
			name: "leading-slash tokens are not paths in PowerShell", tool: "powershell_run",
			command: `findstr /i /c:"x" <EXT>\d.txt`, want: approval.EffectWrite,
			// findstr is not in the read-only cmdlet table, so the deterministic
			// analysis cannot prove an effect and the classifier stub completes
			// it as a write. The point of the case is the fact list: /i and /c:
			// are native-program flags and must never appear as directories.
			wantDirs: []string{`<EXT>\d.txt`, `<EXT>\from-classifier`},
			note:     "leading-slash flag tokens are excluded by the PowerShell dialect policy",
		},
	}
}

func TestShellAssessmentConformancePowerShell(t *testing.T) {
	dirs := newConformanceDirs(t)
	for _, tc := range powershellConformanceCases() {
		t.Run(tc.name, func(t *testing.T) {
			command := dirs.expand(tc.command)[0]
			got := conformanceMods(dirs.ext).assessShellCommand(tc.tool, pathutil.FlavorPowerShell, command, nil, dirs.ws)

			require.Equal(t, tc.want, got.Effect, "effect for %q", command)
			require.Equal(t, slashSet(dirs.expand(tc.wantDirs...)), slashSet(got.KnownDirs),
				"known dirs for %q (%s)", command, tc.note)
		})
	}
}

// TestShellAssessmentScriptExecutionConformancePowerShell pins the PowerShell
// half of the script-review release valve: one bare interpreter and one literal
// workspace script stay opaque with unknown effect, and the binding only
// verifies for a readable text file inside the workspace.
func TestShellAssessmentScriptExecutionConformancePowerShell(t *testing.T) {
	dirs := newConformanceDirs(t)
	target := filepath.Join(dirs.ws, "check.py")
	require.NoError(t, os.WriteFile(target, []byte("print('ok')\n"), 0o600))
	m := &Mods{
		Config: testConfigForWorkspace(dirs.ws),
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.UnknownCommandAssessment()
		},
	}

	got := m.assessShellCommand("powershell_run", pathutil.FlavorPowerShell, "python check.py", nil, dirs.ws)
	require.Equal(t, approval.EffectUnknown, got.Effect)
	facts := got.Reviewability.ScriptExecution
	require.NotNil(t, facts)
	require.True(t, facts.Verified())
	require.Equal(t, filepath.Clean(target), filepath.Clean(facts.ResolvedPath))

	inline := m.assessShellCommand("powershell_run", pathutil.FlavorPowerShell, "python -c 'print(1)'", nil, dirs.ws)
	require.Nil(t, inline.Reviewability.ScriptExecution)
}
