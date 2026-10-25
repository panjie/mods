package commandanalysis

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Independent safety assertions, not derived from the compatibility snapshot.
func TestCatalogSystemMutationSafety(t *testing.T) {
	for _, source := range []string{"date --set=2030-01-01", "date -s tomorrow", "date 102412002030", "date --unknown", "hostname new-host", "hostname -F hosts", "hostname --unknown", "xargs uniq input", "xargs date", "xargs hostname"} {
		shell := AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{})
		args := strings.Fields(source)
		process := AssessArgvStaticWithPolicy(args[0], args[1:], true, ReadOnlyCommandPolicy{})
		for _, result := range []CommandAssessment{shell, process} {
			require.NotEqual(t, EffectRead, result.Effect, source)
			require.NotEqual(t, CoverageComplete, result.WriteCoverage, source)
		}
	}
	for _, source := range []string{"date", "date -u +%F", "date -Iseconds", "date --date=tomorrow", "hostname", "hostname --fqdn"} {
		require.Equal(t, EffectRead, AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{}).Effect, source)
	}
}

func TestCatalogIncompleteWritersStayIncomplete(t *testing.T) {
	for _, source := range []string{"cp input output", "mv input output", "find . -exec touch out \\;", "env PATH=/unknown touch out", "mystery > out", "touch a; mystery b"} {
		r := AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{})
		require.NotEqual(t, EffectRead, r.Effect, source)
		require.NotEqual(t, CoverageComplete, r.WriteCoverage, source)
	}
}

func TestPowerShellExecutableDoesNotInheritAliasRead(t *testing.T) {
	for _, name := range []string{"sort.exe", "sort.cmd", "sort.bat"} {
		f := catalogTokens([]string{name, "-o", "output", "input"}, powershellSpace, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectWrite, f.Effect)
		require.False(t, f.ReadOnly)
		require.False(t, f.BoundedWrite)
	}
	for _, name := range []string{"uniq.exe", "head.exe"} {
		require.False(t, catalogTokens([]string{name, "input"}, powershellSpace, ReadOnlyCommandPolicy{}).ReadOnly)
	}
}

func TestPOSIXNoOpBuiltin(t *testing.T) {
	for _, source := range []string{
		`:`, `: ignored --anything 'x y'`, `: "$value"`,
		`: 2>/dev/null`, `: >/dev/null`, `: < input`,
	} {
		r := AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectRead, r.Effect, source)
		require.Equal(t, CoverageComplete, r.WriteCoverage, source)
		require.Empty(t, r.Gaps, source)
	}
	for _, source := range []string{
		`: > out`, `: >> out`, `: <> out`, `: 2> out`, `: > "$OUT"`,
		`: > NUL`, `: > '/dev/null '`,
		`: "$(touch out)"`, `: < "$(touch out)"`, `: <(touch out)`,
		": <<EOF\n$(touch out)\nEOF",
		`PATH=/tmp :`, `env :`, `xargs :`, `find . -exec : {} \;`,
	} {
		r := AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{})
		require.NotEqual(t, EffectRead, r.Effect, source)
	}
	for _, posix := range []bool{true, false} {
		r := AssessArgvStaticWithPolicy(":", nil, posix, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectUnknown, r.Effect)
	}
}

func TestShellBuiltinDoesNotBecomeNativeProgram(t *testing.T) {
	require.Equal(t, EffectRead, AssessShellStaticWithPolicy("cd /tmp", true, ReadOnlyCommandPolicy{}).Effect)
	for _, source := range []string{"env cd /tmp", "xargs cd", "find . -exec cd {} \\;"} {
		require.NotEqual(t, EffectRead, AssessShellStaticWithPolicy(source, true, ReadOnlyCommandPolicy{}).Effect, source)
	}
	for _, posix := range []bool{true, false} {
		r := AssessArgvStaticWithPolicy("cd", []string{"/tmp"}, posix, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectUnknown, r.Effect)
	}
}
