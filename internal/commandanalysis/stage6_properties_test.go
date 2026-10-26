package commandanalysis

import (
	"context"
	"reflect"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func inertAnalysisEngine() *Engine {
	return NewEngine(Settings{}, Dependencies{Environment: func() []string { return []string{"HOME=/not-the-literal-home"} }, Paths: pathsFunc{}, Git: gitFunc(func(context.Context, GitQuery) (string, error) { return "", nil })})
}

func TestUnknownSiblingNeverInheritsCompleteCoverage(t *testing.T) {
	for _, prefix := range []string{"touch out", "uniq input out", "env LANG=C touch out", "mkdir -p out/child", "cat input > out"} {
		for _, source := range []string{prefix + "; mods_unknown_effect", "mods_unknown_effect; " + prefix, prefix + " | mods_unknown_effect", prefix + " && mods_unknown_effect"} {
			r, err := inertAnalysisEngine().Analyze(context.Background(), shellInput(source))
			require.NoError(t, err)
			require.Equal(t, CoverageIncomplete, r.WriteCoverage, source)
		}
	}
}

func FuzzCommandAnalysisNoPanic(f *testing.F) {
	for _, s := range []string{awkCodeCountPipeline, codeCountCommand, packageQueryLoop, "touch out", "cat $(unknown)", "env LANG=C uniq -f2 input", "git -C . push origin", "if true; then touch out; fi", "\x00", "'unterminated"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 2048 {
			t.Skip()
		}
		engine := inertAnalysisEngine()
		a, err := engine.Analyze(context.Background(), shellInput(source))
		if err != nil {
			return
		}
		b, err := engine.Analyze(context.Background(), shellInput(source))
		require.NoError(t, err)
		require.Equal(t, a, b, "fresh analyses with immutable dependencies must be deterministic")
		require.True(t, reflect.DeepEqual(a.Clone().Clone(), a.Clone()))
		// Force an unknown top-level command after any comments in the prefix.
		mixed, err := engine.Analyze(context.Background(), shellInput(source+"\nmods_unknown_effect"))
		require.NoError(t, err)
		require.NotEqual(t, CoverageComplete, mixed.WriteCoverage)
	})
}

func FuzzAuditedOptionsNeverHideUnknownFlag(f *testing.F) {
	for _, s := range []string{"file", "-am", "--reference=input", "-r", "", "--"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, arg string) {
		if len(arg) > 1024 {
			t.Skip()
		}
		for _, name := range []string{"touch", "mkdir", "rm", "rmdir", "tee", "unlink", "uniq", "xxd"} {
			facts := catalogTokens([]string{name, "--mods-unmodeled-option", arg, "out"}, nativeSpace, ReadOnlyCommandPolicy{})
			require.False(t, facts.BoundedWrite)
			require.False(t, facts.BuiltinRead)
		}
		for _, name := range []string{"Import-Csv", "ForEach-Object"} {
			facts := catalogTokens([]string{name, "-ModsUnmodeledOption", arg}, powershellSpace, ReadOnlyCommandPolicy{})
			require.False(t, facts.BuiltinRead)
			require.Equal(t, CoverageIncomplete, facts.Coverage)
		}
	})
}

func FuzzLiteralArgvAndMergeStability(f *testing.F) {
	for _, s := range []string{"$HOME/input", "file with spaces", "name[1]", "a'quote", "../file", "https://example.com/repo"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, arg string) {
		if len(arg) > 1024 || arg == "" {
			t.Skip()
		}
		r, err := inertAnalysisEngine().Analyze(context.Background(), Input{Cwd: "/workspace", Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "uniq", Args: []string{"--", arg, "output"}}})
		if err == nil && r.Effect != EffectUnknown {
			found := false
			for _, target := range r.Targets {
				if target.Role == TargetRead {
					found = true
					require.Equal(t, arg, target.Raw)
				}
			}
			require.True(t, found)
		}
		first := NormalizeRemoteOrigins([]string{arg, arg, "https://example.com/path"})
		require.Equal(t, first, NormalizeRemoteOrigins(append(append([]string{}, first...), first...)))
	})
}
