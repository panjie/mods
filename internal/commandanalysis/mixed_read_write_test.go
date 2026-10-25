package commandanalysis

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

const codeCountCommand = `git ls-files -z '*.go' | xargs -0 cat > /tmp/all_go.txt
echo "total lines: $(wc -l < /tmp/all_go.txt)"
echo "blank: $(grep -c '^\s*$' /tmp/all_go.txt)"
echo "comment: $(grep -c '^\s*//' /tmp/all_go.txt)"
echo "code-ish: $(( $(wc -l < /tmp/all_go.txt) - $(grep -c '^\s*$' /tmp/all_go.txt) - $(grep -c '^\s*//' /tmp/all_go.txt) ))"`

func TestMixedReadSubstitutionsKeepWriteCoverage(t *testing.T) {
	for _, source := range []string{codeCountCommand, `printf x > out; echo "$(wc -l < out)"`, `printf x > out; echo "$(cat out)"`, `printf x > out; echo "$((1 + 2 * 3))"`} {
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
		require.NoError(t, err)
		require.Equal(t, EffectWrite, r.Effect, source)
		require.Equal(t, CoverageComplete, r.WriteCoverage, source)
		require.Empty(t, r.Gaps)
		require.Empty(t, r.Diagnostics)
		writes := 0
		for _, target := range r.Targets {
			if target.Role == TargetWrite {
				writes++
				require.Equal(t, TargetResolved, target.State)
			}
		}
		require.Equal(t, 1, writes, source)
	}
}

func TestMixedReadSubstitutionsPreserveUnprovenEffects(t *testing.T) {
	for _, source := range []string{
		`printf x > out; echo "$(touch other)"`,
		`printf x > out; echo "$(cat in > other)"`,
		`printf x > out; echo "$(mystery)"`,
		`printf x > out; cat <(touch other)`,
		`printf x > out; echo "$((PATH = 1))"`,
		`printf x > out; echo "$(( $(cat in) ))"`,
		`printf x > out; echo "${OTHER:=value}"`,
		`printf x > out; echo "$(( $(wc -l in) ))"`,
		`printf x > out; echo "$(( $(grep -c pattern in other) ))"`,
	} {
		r := AssessShellStaticWithContext(source, true, ReadOnlyCommandPolicy{}, t.TempDir())
		require.NotEqual(t, EffectRead, r.Effect, source)
		require.Equal(t, CoverageIncomplete, r.WriteCoverage, source)
		if source == `printf x > out; echo "$(touch other)"` || source == `printf x > out; echo "$(cat in > other)"` {
			found := false
			for _, target := range r.Targets {
				if target.Role == TargetWrite && target.Raw == "other" {
					found = true
				}
			}
			require.True(t, found, "nested write must remain visible")
		}
	}
}

func TestCodeCountDirectoryChangeExplained(t *testing.T) {
	r := AssessShellStaticWithContext("cd /project\n"+codeCountCommand, true, ReadOnlyCommandPolicy{}, "/project")
	require.Equal(t, []AssessmentGap{GapPathContext}, r.Gaps)
	require.Contains(t, r.Diagnostics, "Working directory changes inside the command; use the tool cwd argument for a fixed directory")
}
