package commandanalysis

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestPartitionShellAnalysisPathsSeparatesRuntimeExpressions(t *testing.T) {
	concrete, unresolved := partitionShellAnalysisPaths(
		[]string{`C:\Users\Test\Documents`, `$PROFILE.CurrentUserCurrentHost`, `$target`},
		nil,
		pathutil.FlavorPowerShell,
	)
	require.Equal(t, []string{`C:\Users\Test\Documents`}, concrete)
	require.ElementsMatch(t, []string{`$PROFILE.CurrentUserCurrentHost`, `$target`}, unresolved)
}
func TestNormalizeLiteralProcessPathDoesNotExpandShellSyntax(t *testing.T) {
	cwd := canonicalTestPath(t, t.TempDir())
	require.Equal(t, filepath.Join(cwd, "$HOME", "out.txt"), normalizeLiteralProcessPath("$HOME/out.txt", cwd, pathutil.FlavorPOSIX))
	require.Equal(t, filepath.Join(cwd, "~", "out.txt"), normalizeLiteralProcessPath("~/out.txt", cwd, pathutil.FlavorPOSIX))
}
func TestCommandMutatesPOSIXEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}
	mutations := []string{
		`FOO=/x cmd`,
		`FOO=/x; cmd`,
		`export FOO=/x`,
		`declare FOO=/x`,
		`local FOO=/x`,
		`unset FOO`,
		`read FOO`,
		`mapfile -t FOO`,
		`for f in *; do echo $f; done`,
		`f() { local X=1; }; f`,
		`env FOO=/x cmd`,
		`set -- a b`,
		`shift`,
		`(( COUNT += 1 ))`,
		`cat <&0 2>err`, // unparseable or redirection-heavy input fails closed
		`if [ -n "$X" ]; then Y=1; fi`,
	}
	for _, command := range mutations {
		require.True(t, commandMutatesPOSIXEnvironment(command), command)
	}

	reads := []string{
		`cat "$FOO/x"`,
		`echo done && ls /tmp`,
		`git status --short | head -3`,
		`printf '%s\n' "$FOO"`,
	}
	for _, command := range reads {
		require.False(t, commandMutatesPOSIXEnvironment(command), command)
	}
}

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	cleaned := filepath.Clean(path)
	if eval, err := filepath.EvalSymlinks(cleaned); err == nil {
		return filepath.Clean(eval)
	}
	return cleaned
}
func TestShellExternalPathFactsKeepsSpacedWindowsPath(t *testing.T) {
	ws := filepath.Clean(t.TempDir())

	got := shellExternalPathFacts([]string{`C:\Program Files\App\notes.txt`}, ws, pathutil.FlavorPowerShell)
	require.Equal(t, []string{`C:\Program Files\App\notes.txt`}, got)
	require.NotContains(t, got, `C:\Program`)
}
func TestShellExternalPathFactsPowerShellDropsUnixStyleTokens(t *testing.T) {
	got := shellExternalPathFacts([]string{"/out", "/reference", "/etc/passwd"}, "", pathutil.FlavorPowerShell)
	require.Empty(t, got)
}
func TestShellExternalPathFactsPOSIXKeepsNonExplicitExternals(t *testing.T) {
	ws := filepath.Clean(t.TempDir())

	got := shellExternalPathFacts([]string{`\\server\share\f`}, ws, pathutil.FlavorPOSIX)
	require.Len(t, got, 1)
	require.Equal(t, pathutil.LocationExternal, pathutil.Location(got[0], ws, nil))
}
func TestFinalizeShellAnalysisPreservesDynamicReadEffect(t *testing.T) {
	got := finalizeCommandAssessment(
		CommandAssessment{
			Evidence:       EvidenceStatic,
			Effect:         EffectRead,
			Reason:         "inspects runtime path",
			DynamicTargets: []string{"$target"},
		},
		pathutil.FlavorPOSIX,
	)

	require.Equal(t, EffectRead, got.Effect)
	require.Empty(t, got.KnownDirs)
	require.Equal(t, []string{"$target"}, got.DynamicTargets)
}
func TestLiteralArgLooksPathLikeDialect(t *testing.T) {
	for _, tc := range []struct {
		name   string
		arg    string
		flavor pathutil.Flavor
		want   bool
	}{
		{"POSIX absolute path", "/etc/passwd", pathutil.FlavorPOSIX, true},
		{"POSIX single-segment path", "/d", pathutil.FlavorPOSIX, true},
		{"PowerShell switch", "/d", pathutil.FlavorPowerShell, false},
		{"PowerShell compiler switch with value", `/out:C:\out\a.exe`, pathutil.FlavorPowerShell, false},
		{"PowerShell Unix-looking switch", "/etc/passwd", pathutil.FlavorPowerShell, false},
		{"PowerShell UNC with forward slashes", "//server/share", pathutil.FlavorPowerShell, true},
		{"PowerShell drive-absolute literal", `C:\tools\emacs\bin\emacs.exe,0`, pathutil.FlavorPowerShell, true},
		{"PowerShell parent traversal", `..\outside.txt`, pathutil.FlavorPowerShell, true},
		{"PowerShell home expansion", `~/notes.txt`, pathutil.FlavorPowerShell, true},
		{"PowerShell bare name", "Icon", pathutil.FlavorPowerShell, false},
		{"POSIX relative slash", "sub/file", pathutil.FlavorPOSIX, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, literalArgLooksPathLike(tc.arg, tc.flavor))
		})
	}
}
