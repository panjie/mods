//go:build windows

package approval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Also selected by the existing ps51/ps7 Windows reliability matrix. Missing
// hosts are failures, not skipped coverage.
func TestWindowsReliabilityCommandConstraint(t *testing.T) {
	host, err := exec.LookPath(getWindowsShellPath())
	require.NoError(t, err)
	if expected := os.Getenv("MODS_EXPECT_POWERSHELL_HOST"); expected != "" {
		require.Equal(t, strings.ToLower(expected), strings.ToLower(filepath.Base(host)))
	}
	for _, command := range []string{
		`Set-Location $env:TEMP; New-Item -ItemType Directory x; foreach ($f in 'a.lua','b.lua') { Invoke-WebRequest 'https://raw.githubusercontent.com' -OutFile $f }; Get-ChildItem`,
		`'a','b' | ForEach-Object { Set-Content $_ 'x' }`,
		`powershell.exe -EncodedCommand ZQB4AGkAdAA=`,
		`& .\generated.ps1`,
		`& .\generated.cmd`,
		`& $command`,
		`python -c 'print(1)'`,
	} {
		require.True(t, AssessShellStaticWithPolicy(command, false, ReadOnlyCommandPolicy{}).RequiresSimplification(), command)
	}
	for _, command := range []string{`Get-ChildItem | Select-Object -First 5`, `Get-Content 'a;b.txt'`, `git status`} {
		a := AssessShellStaticWithPolicy(command, false, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectRead, a.Effect, command)
		require.False(t, a.RequiresSimplification(), command)
	}
}
