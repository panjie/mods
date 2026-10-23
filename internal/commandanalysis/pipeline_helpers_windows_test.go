//go:build windows

package commandanalysis

import (
	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
	"os"
	"strings"
	"testing"
)

func TestResolvePowerShellEnvTargets(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")
	t.Setenv("STARSHIP_CONFIG", "") // pin the unset case against dev machines

	known, dynamic := resolvePowerShellEnvTargets(nil, []string{`$env:SystemRoot`, `$env:SystemRoot\WinSxS`}, `C:\ws`, `Get-ChildItem "$env:SystemRoot\WinSxS"`, nil, true)
	require.Len(t, known, 1)
	require.True(t, strings.EqualFold(known[0], systemRoot+`\WinSxS`), known)
	require.Empty(t, dynamic, "the bare variable reference is subsumed by its expanded path form")

	known, dynamic = resolvePowerShellEnvTargets(nil, []string{`$env:STARSHIP_CONFIG`, `$env:SystemRoot\WinSxS`}, `C:\ws`, `Get-ChildItem "$env:SystemRoot\WinSxS"`, nil, true)
	require.Len(t, known, 1)
	require.Equal(t, []string{`$env:STARSHIP_CONFIG`}, dynamic, "unset variable references stay dynamic")

	known, dynamic = resolvePowerShellEnvTargets([]string{known[0]}, []string{`$env:SystemRoot\WinSxS`}, `C:\ws`, `Get-ChildItem "$env:SystemRoot\WinSxS"`, nil, true)
	require.Len(t, known, 1, "already-known concrete dirs are not duplicated")
	require.Empty(t, dynamic)

	known, dynamic = resolvePowerShellEnvTargets(nil, []string{`$env:SECRET\x`}, `C:\ws`, `Get-Content "$env:SECRET\x"`, nil, true)
	require.Empty(t, known)
	require.Equal(t, []string{`$env:SECRET\x`}, dynamic)

	known, dynamic = resolvePowerShellEnvTargets([]string{`C:\ws`}, nil, `C:\ws`, ``, nil, true)
	require.Equal(t, []string{`C:\ws`}, known)
	require.Nil(t, dynamic)
}
func TestResolvePowerShellEnvTargetsBareReferencePolicy(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")

	// A bare reference with a genuine value-shaped use expands to the
	// variable's directory value for reads.
	known, dynamic := resolvePowerShellEnvTargets(nil, []string{`$env:SystemRoot`}, `C:\ws`, `Get-ChildItem $env:SystemRoot`, nil, true)
	require.Len(t, known, 1)
	require.True(t, strings.EqualFold(known[0], systemRoot), known)
	require.Empty(t, dynamic)

	// The same value-shaped use on a write keeps the reference dynamic: the
	// corrective loop still applies to unbounded write targets.
	known, dynamic = resolvePowerShellEnvTargets(nil, []string{`$env:SystemRoot`}, `C:\ws`, `Get-ChildItem $env:SystemRoot`, nil, false)
	require.Empty(t, known)
	require.Equal(t, []string{`$env:SystemRoot`}, dynamic)

	// A name shadowed by this call's secret environment never expands: the
	// child shell would observe the secret's value.
	t.Setenv("MODS_TEST_SECRET_DIR", `C:\shadowed`)
	known, dynamic = resolvePowerShellEnvTargets(nil, []string{`$env:MODS_TEST_SECRET_DIR`}, `C:\ws`, `Get-ChildItem $env:MODS_TEST_SECRET_DIR`, map[string]bool{"MODS_TEST_SECRET_DIR": true}, true)
	require.Empty(t, known)
	require.Equal(t, []string{`$env:MODS_TEST_SECRET_DIR`}, dynamic)
}
func TestResolvePowerShellEnvTargetsFlattenedArrayToken(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")

	command := `Remove-Item "$env:SystemRoot\a.wav","$env:SystemRoot\b.wav" -ErrorAction SilentlyContinue`
	known, dynamic := resolvePowerShellEnvTargets(nil, []string{`$env:SystemRoot\a.wav","$env:SystemRoot\b.wav`}, `C:\ws`, command, nil, false)
	require.Empty(t, dynamic, "the flattened array token resolves through its textual path uses")
	require.Len(t, known, 2)
	require.True(t, strings.EqualFold(known[0], systemRoot+`\a.wav`), known)
	require.True(t, strings.EqualFold(known[1], systemRoot+`\b.wav`), known)
}
func TestProcessLiteralPathFilterDropsPowerShellProviderTargets(t *testing.T) {
	cwd := t.TempDir()
	dirs := filterLiteralArgPaths([]string{
		"Set-ItemProperty",
		`HKCU:\Software\Classes\Neovide`,
		`C:\real\file.txt`,
	}, cwd, pathutil.FlavorPowerShell)

	require.Equal(t, []string{`C:\real\file.txt`}, dirs)
}
