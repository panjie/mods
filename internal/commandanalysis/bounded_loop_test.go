package commandanalysis

import (
	"context"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

const packageQueryLoop = `for p in dust duf; do pacman -Si $p 2>/dev/null | grep -E '^(Name|Version|Description)'; done; echo '--- installed?'; pacman -Q ncdu gdu dust duf btop 2>&1`

func TestBoundedReadOnlyLoops(t *testing.T) {
	for _, source := range []string{
		packageQueryLoop,
		`for p in dust duf; do pacman -Si "$p"; done`,
		`for p in dust duf; do printf '%s\n' "$p"; done`,
		`for p in 'space name' dust; do cat "$p"; done`,
		`for pat in 'a' 'b'; do :; done`,
		`git ls-files -z -- '*.go' | xargs -0 wc -l | tail -1
 echo '--- split ---'
 for pat in '*.go:internal/*' '*.go:internal/*_test.go' '*.go:tests/*'; do :; done
 printf 'prod go (non-test): '; git ls-files -z -- '*.go' | grep -zv '_test\.go$' | xargs -0 wc -l | tail -1
 printf 'test go: '; git ls-files -z -- '*.go' | grep -z '_test\.go$' | xargs -0 wc -l | tail -1`,
	} {
		t.Run(source, func(t *testing.T) {
			r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
			require.NoError(t, err)
			require.Empty(t, r.Diagnostics)
			require.Equal(t, EffectRead, r.Effect)
			require.Equal(t, EvidenceStatic, r.Evidence)
			require.Equal(t, CoverageComplete, r.WriteCoverage)
			require.Empty(t, r.Gaps)
		})
	}
}
func TestBoundedLoopRejectsUnprovenEffects(t *testing.T) {
	sources := []string{
		`for p in dust; do : > "$p"; done`,
		`for p in dust; do :; done > out`,
		`for p in dust; do : "$(touch out)"; done`,
		`for p in dust; do printf x; done > NUL`,
		`for p in dust; do printf x; done > '/dev/null '`,
		`for p in dust; do printf x >& file; done`,
		"for p in " + strings.Repeat("dust ", maxBoundedLoopItems) + "; do " + strings.Repeat("pacman -Si dust; ", 10) + "done",
		`for p in $(touch /tmp/out); do pacman -Si "$p"; done`,
		`for p in $packages; do pacman -Si "$p"; done`,
		`for p in *; do pacman -Si "$p"; done`,
		`for p; do pacman -Si "$p"; done`,
		`for p in dust; do pacman -Sy "$p"; done`,
		`for p in dust --refresh; do pacman -Si "$p"; done`,
		`for p in dust; do touch "$p"; done`,
		`for p in dust; do printf x > "$p"; done`,
		`for p in dust; do printf x > 'NUL'; done`,
		`for p in dust; do pacman -Si "$p" & done`,
		`for p in dust; do p=--refresh; pacman -Si "$p"; done`,
		`for p in dust; do read p; pacman -Si "$p"; done`,
		`for p in dust; do for q in duf; do pacman -Si "$q"; done; done`,
		`for PATH in /tmp; do pacman -Si dust; done`,
		`IFS=x; for p in dust; do pacman -Si $p; done`,
		`for p in 'dust --refresh'; do pacman -Si $p; done`,
		`for p in dust; do pacman -Si "${p:-$(touch /tmp/out)}"; done`,
		`for p in dust; do $p -Si duf; done`,
		`for p in dust; do mystery "$p"; done`,
		"for p in " + strings.Repeat("dust ", maxBoundedLoopItems+1) + `; do pacman -Si "$p"; done`,
	}
	for _, source := range sources {
		t.Run(source, func(t *testing.T) {
			r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: t.TempDir(), Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: source}})
			require.NoError(t, err)
			require.NotEqual(t, EffectRead, r.Effect)
			require.Equal(t, CoverageIncomplete, r.WriteCoverage)
			require.NotEmpty(t, r.Diagnostics)
		})
	}
}
