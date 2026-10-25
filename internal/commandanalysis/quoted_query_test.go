package commandanalysis

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
	"mvdan.cc/sh/v3/syntax"
)

const anchoredPackageQueryLoop = `for p in gdu dust dua-cli btop bottom nvtop pacseek paru yay paccache bleachbit stacer tui-journal; do printf '%s: ' "$p"; pacman -Ss "^$p\$" | head -1; echo; done`

func TestQuotedLoopWordDecoding(t *testing.T) {
	for _, tc := range []struct {
		word, want string
		ok         bool
	}{
		{`"^$p\$"`, "^gdu$", true},
		{`'^$p$'`, "^$p$", true},
		{`"a\.\$"`, `a\.$`, true},
		{`"a\\b"`, `a\b`, true},
		{`"a\"b"`, `a"b`, true},
		{"\"\\`literal\\`\"", "`literal`", true},
		{`"^[a-z]+$"`, `^[a-z]+$`, true},
		{"\"a\\\nb\"", "ab", true},
		{`"$missing"`, "", false},
		{`"${p:-fallback}"`, "", false},
		{`"$(printf gdu)"`, "", false},
		{`*`, "", false},
	} {
		t.Run(tc.word, func(t *testing.T) {
			file, err := parsePOSIX("printf " + tc.word)
			require.NoError(t, err)
			word := file.Stmts[0].Cmd.(*syntax.CallExpr).Args[1]
			got, ok := boundLoopWord(word, "p", "gdu", false)
			require.Equal(t, tc.ok, ok)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestPacmanQuotedQueryEvidence(t *testing.T) {
	engine := NewEngine(Settings{}, Dependencies{})
	for _, tc := range []struct {
		source string
		read   bool
	}{
		{anchoredPackageQueryLoop, true},
		{`pacman -Ss '^gdu$'`, true},
		{`pacman -Ss "^gdu\$"`, true},
		{`pacman -Ss '^[a-z]+$'`, true},
		{`pacman -Ss "$pattern"`, false},
		{`pacman -Ss $(printf gdu)`, false},
		{`pacman -Ss *`, false},
		{`pacman -Ss --refresh '^gdu$'`, false},
		{`for p in gdu --refresh; do pacman -Ss "$p"; done`, false},
	} {
		t.Run(tc.source, func(t *testing.T) {
			r, err := engine.Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: tc.source}})
			require.NoError(t, err)
			require.Equal(t, tc.read, r.Effect == EffectRead)
			if tc.read {
				require.Equal(t, EvidenceStatic, r.Evidence)
				require.Empty(t, r.Diagnostics)
				require.Equal(t, CoverageComplete, r.WriteCoverage)
			}
		})
	}
	for _, pattern := range []string{"^gdu$", `^[a-z]+$`, `a\.b`, "$args", "*", "$(touch /tmp/never-executed)"} {
		r, err := engine.Analyze(context.Background(), Input{Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "pacman", Args: []string{"-Ss", pattern}}})
		require.NoError(t, err)
		require.Equal(t, EffectRead, r.Effect, "literal argv: %q", pattern)
		weak := catalogTokens([]string{"pacman", "-Ss", pattern}, nativeSpace, ReadOnlyCommandPolicy{})
		require.False(t, weak.BuiltinRead, "legacy projections lack literal evidence")
	}
}

func TestLoopFailureRetainsReason(t *testing.T) {
	for _, tc := range []struct{ source, reason string }{
		{`for p in dust; do pacman -Ss "$missing"; done`, "Loop p: unresolved or unsupported argument in pacman"},
		{`for p in dust; do pacman -Siy "$p"; done`, "Loop p: command or arguments are not proven read-only: pacman"},
		{`for p in $packages; do pacman -Ss "$p"; done`, "Loop p: iteration list contains an unresolved expansion"},
	} {
		r := AssessShellStaticWithPolicy(tc.source, true, ReadOnlyCommandPolicy{})
		require.NotEqual(t, EffectRead, r.Effect)
		require.Contains(t, r.Diagnostics, tc.reason)
	}
}
