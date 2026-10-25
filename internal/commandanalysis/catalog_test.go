package commandanalysis

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

// Captured from 70b2737 in an isolated worktree, before catalog migration.
// No update flag: differences must be reviewed rather than re-recorded.
func TestCatalogCompatibilityEntries(t *testing.T) {
	type fixture struct {
		Name    string
		Posix   bool
		Args    []string
		Read    bool
		Known   bool
		Risk    bool
		Bounded bool
		Paths   []string
		Modes   []uint8
	}
	raw, err := os.ReadFile("testdata/catalog_legacy.json")
	require.NoError(t, err)
	var cases []fixture
	require.NoError(t, json.Unmarshal(raw, &cases))
	covered := map[catalogKey]bool{}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%t/%s/%v", c.Posix, c.Name, c.Args), func(t *testing.T) {
			space := spaceForShell(c.Posix)
			covered[catalogKey{space, c.Name}] = true
			f := catalogTokens(append([]string{c.Name}, c.Args...), space, ReadOnlyCommandPolicy{})
			read := c.Read
			// Independently fixed false reads; the legacy fixture stays unchanged.
			if c.Posix && (c.Name == "date" || c.Name == "hostname") && len(c.Args) > 0 {
				read = false
			}
			require.Equal(t, read, f.ReadOnly, "read")
			require.Equal(t, c.Known, f.KnownWrite, "known write")
			require.Equal(t, c.Risk, f.Risky, "risky")
			bounded := c.Bounded
			// unlink accepts exactly one operand; the immutable legacy corpus
			// contains a two-operand call formerly labeled complete.
			if c.Posix && c.Name == "unlink" && len(c.Args) > 1 {
				bounded = false
			}
			// Reviewed stage-5 -- binding: the old extractor dropped -output.
			dashOperand := c.Posix && len(c.Args) == 2 && c.Args[0] == "--" && c.Args[1] == "-output" && (c.Name == "touch" || c.Name == "mkdir" || c.Name == "rm" || c.Name == "rmdir" || c.Name == "tee" || c.Name == "unlink")
			if dashOperand {
				bounded = true
			}
			// Stage-5 scalar cmdlet audit narrows legacy unbound/unknown forms.
			if !c.Posix && (c.Name == "add-content" || c.Name == "ac" || c.Name == "set-content" || c.Name == "sc") && (len(c.Args) == 0 || strings.HasPrefix(c.Args[0], "-")) {
				bounded = false
			}
			if !c.Posix && c.Name == "out-file" && len(c.Args) > 1 {
				bounded = false
			}
			require.Equal(t, bounded, f.BoundedWrite, "bounded")
			var paths []string
			var modes []uint8
			for _, w := range f.Writes {
				paths = append(paths, w.path)
				modes = append(modes, uint8(w.mode))
			}
			if dashOperand {
				c.Paths = []string{"-output"}
				c.Modes = []uint8{uint8(writableTargetParent)}
				if c.Name == "rmdir" {
					c.Modes = []uint8{uint8(writableTargetDirectory)}
				}
			}
			require.Equal(t, c.Paths, paths)
			require.Equal(t, c.Modes, modes)
		})
	}
	for key := range commandCatalog.entries {
		require.True(t, covered[key], "missing independent fixture for %+v", key)
	}
}

func TestCatalogTextOutput(t *testing.T) {
	cases := []struct {
		args     []string
		effect   CommandEffect
		complete bool
		output   string
	}{
		{[]string{"uniq"}, EffectRead, true, ""},
		{[]string{"uniq", "-f", "2", "input file"}, EffectRead, true, ""},
		{[]string{"uniq", "input file", "output file"}, EffectWrite, true, "output file"},
		{[]string{"uniq", "--", "-input", "-output"}, EffectWrite, true, "-output"},
		{[]string{"uniq", "--unknown", "input"}, EffectUnknown, false, ""},
		{[]string{"uniq", "-f"}, EffectUnknown, false, ""},
		{[]string{"uniq", "--skip-fields=2", "input"}, EffectRead, true, ""},
		{[]string{"uniq", "a", "b", "c"}, EffectUnknown, false, ""},
		{[]string{"xxd", "-c", "16", "input"}, EffectRead, true, ""},
		{[]string{"xxd", "-r", "input"}, EffectWrite, false, ""},
		{[]string{"xxd", "-r", "input", "output"}, EffectWrite, true, "output"},
		{[]string{"xxd", "-revert", "input", "output"}, EffectWrite, true, "output"},
		{[]string{"xxd", "--unknown"}, EffectUnknown, false, ""},
		{[]string{"xxd", "-c"}, EffectUnknown, false, ""},
		{[]string{"xxd", "-r", "--unknown"}, EffectWrite, false, ""},
		{[]string{"xxd", "-r", "input", ""}, EffectWrite, false, ""},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			f := catalogTokens(c.args, nativeSpace, ReadOnlyCommandPolicy{})
			require.Equal(t, c.effect, f.Effect)
			require.Equal(t, c.complete, f.Coverage == CoverageComplete)
			if c.output != "" {
				require.Contains(t, f.Targets, InvocationTarget{Path: c.output, Role: TargetWrite, Operation: OperationFollowLeaf})
			}
			result := AssessArgvStaticWithPolicy(c.args[0], c.args[1:], true, ReadOnlyCommandPolicy{})
			require.Equal(t, c.effect, result.Effect)
			require.Equal(t, c.complete, result.WriteCoverage == CoverageComplete)
		})
	}
}

func TestCatalogIdentityBoundaries(t *testing.T) {
	for _, name := range []string{"Set-Content", "sc", "Remove-Item", "ri", "Get-Content", "gc"} {
		process := AssessArgvStaticWithPolicy(name, []string{"-Path", `C:\safe\out`}, false, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectUnknown, process.Effect, name)
		require.NotEqual(t, CoverageComplete, process.WriteCoverage, name)
	}
	for _, name := range []string{"Set-Content.exe", "gc.exe", "Get-Content.cmd", "Get-Content.bat", `C:\evil\Get-Content`, `Module\Get-Content`} {
		f := catalogTokens([]string{name, "x"}, powershellSpace, ReadOnlyCommandPolicy{})
		require.False(t, f.ReadOnly, name)
		require.False(t, f.BoundedWrite, name)
	}
	alias := catalogTokens([]string{"rm", "-LiteralPath", `C:\safe\out`}, powershellSpace, ReadOnlyCommandPolicy{})
	native := catalogTokens([]string{"rm.exe", "--", `C:\safe\out`}, powershellSpace, ReadOnlyCommandPolicy{})
	require.Equal(t, "powershell/remove-item", alias.EntryID)
	require.Equal(t, "native/rm", native.EntryID)
	require.Equal(t, []writableTarget{{path: `C:\safe\out`, mode: writableTargetParent}}, native.Writes)
	for _, program := range []string{"/tmp/uniq", `C:\tmp\uniq.exe`, "./cat"} {
		result := AssessArgvStaticWithPolicy(program, []string{"input"}, false, ReadOnlyCommandPolicy{Commands: []string{"uniq", "cat"}})
		require.Equal(t, EffectUnknown, result.Effect)
	}
}

func TestSemanticSessionOneEvaluationPerNode(t *testing.T) {
	for _, source := range []string{
		"cat a; touch b; uniq in out; mystery x", "env LC_ALL=C trusted a", "cat $(cat file)",
		"touch \"$HOME/out\"; uniq in \"$HOME/out\"", "git status; git push https://example.com/repo.git",
		"trusted a > out; mystery b", "if cat a; then touch b; fi",
	} {
		t.Run(source, func(t *testing.T) {
			run := NewEngine(Settings{}, Dependencies{Environment: func() []string { return nil }, Paths: pathsFunc{}}).run(context.Background())
			p := run.parse(source, pathutil.FlavorPOSIX)
			policy := ReadOnlyCommandPolicy{Commands: []string{"trusted"}}
			p.assess(policy, ArgvStaticContext{run: run})
			invocations, ok := p.invocations()
			require.True(t, ok)
			// Exercise all consumers, including evidence without trust and Git's
			// context invalidation, against the same syntax-node facts.
			policy.session = p.semantics
			assessPOSIXFile(p.file, policy, ArgvStaticContext{run: run, semantics: p.semantics})
			assessPOSIXFile(p.file, ReadOnlyCommandPolicy{session: p.semantics}, ArgvStaticContext{run: run, semantics: p.semantics})
			run.resolveParsedGitPushOrigins(p, "")
			require.Equal(t, len(invocations), p.semantics.evaluations)
			old := p.semantics
			p.assess(policy, ArgvStaticContext{run: run})
			require.NotSame(t, old, p.semantics, "fresh assessment must not reuse semantic results")
		})
	}
	ir := &psBridgeIR{Invocations: []psCommandInvocation{{NodeID: 1, Name: "Get-Content", Args: []string{"a"}}, {NodeID: 2, Name: "Set-Content", Args: []string{"b", "value"}}, {NodeID: 3, Name: "trusted"}}}
	p := &parsedCommand{source: "synthetic IR", ir: ir}
	p.assess(ReadOnlyCommandPolicy{Commands: []string{"trusted"}}, ArgvStaticContext{})
	require.Equal(t, 3, p.semantics.evaluations)
}

func TestCatalogTrustDoesNotHideOtherEffects(t *testing.T) {
	policy := ReadOnlyCommandPolicy{Commands: []string{"trusted", "uniq"}}
	for _, source := range []string{"trusted a", "env LC_ALL=C trusted a", "uniq input output"} {
		r := AssessShellStaticWithPolicy(source, true, policy)
		require.Equal(t, EffectRead, r.Effect)
		require.Equal(t, EvidenceTrust, r.Evidence)
	}
	for _, source := range []string{"trusted a > out", "trusted a; mystery b", "trusted $(mystery b)", "env PATH=/tmp trusted a"} {
		r := AssessShellStaticWithPolicy(source, true, policy)
		require.NotEqual(t, EffectRead, r.Effect, source)
	}
	f := catalogTokens([]string{"trusted"}, nativeSpace, ReadOnlyCommandPolicy{})
	require.Equal(t, EffectUnknown, f.Effect)
	require.Empty(t, f.EntryID)
}

func TestCatalogNestedEvaluationBudget(t *testing.T) {
	for _, suffix := range []string{"cat x", "touch x", "trusted x"} {
		args := strings.Fields(strings.Repeat("env ", maxAnalysisDepth+1) + suffix)
		f := catalogTokens(args, nativeSpace, ReadOnlyCommandPolicy{Commands: []string{"trusted"}})
		require.False(t, f.ReadOnly)
		require.False(t, f.BoundedWrite)
	}
}

func TestCatalogNestedInvocationsEvaluateOnce(t *testing.T) {
	ev := newCatalogEvaluation(ReadOnlyCommandPolicy{Commands: []string{"trusted"}})
	args := append(strings.Fields(strings.Repeat("env ", 20)), "find", ".", "-exec", "cat", "{}", ";")
	f := ev.tokens(args, nativeSpace)
	require.True(t, f.ReadOnly)
	require.Equal(t, 22, ev.evaluations, "env read/write/risk and find read/write/risk share each nested result")
	ev.tokens(args, nativeSpace)
	require.Equal(t, 22, ev.evaluations)
	// An unrelated configured name must not convert built-in evidence to trust.
	f = ev.tokens([]string{"env", "cat", "x"}, nativeSpace)
	require.Equal(t, EvidenceStatic, f.Evidence)
	require.NotContains(t, f.ReadReason, "user-configured")
}
