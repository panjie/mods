package commandanalysis

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestStage5TextAndWrapperGrammar(t *testing.T) {
	for _, command := range []string{"uniq --skip-fields=2 input", "uniq -f2 input", "uniq -s0 -w4 input", "env LANG=C uniq --check-chars=3 input", "find . -exec uniq -f2 {} ';'"} {
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), shellInput(command))
		require.NoError(t, err)
		require.Equal(t, EffectRead, r.Effect, command)
	}
	for _, command := range []string{"uniq --skip-fields= input", "uniq -f-1 input", "uniq -f output", "uniq -f", "uniq --unknown input", "uniq -f2 a b c", "env LANG=C mystery input", "env $PROGRAM input", "xargs uniq -f2", "find . -exec uniq -f2 {} +", "find . -exec xxd {} +", "find . -exec mystery {} ';'"} {
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), shellInput(command))
		require.NoError(t, err)
		require.NotEqual(t, EffectRead, r.Effect, command)
		require.NotEqual(t, CoverageComplete, r.WriteCoverage, command)
	}
}
func TestStage5FileGrammar(t *testing.T) {
	for _, name := range []string{"touch", "mkdir", "rm", "rmdir", "tee", "unlink"} {
		f := catalogTokens([]string{name, "--", "-output"}, nativeSpace, ReadOnlyCommandPolicy{})
		require.True(t, f.BoundedWrite, name)
		require.Len(t, f.Targets, 1)
		require.Equal(t, "-output", f.Targets[0].Path)
		f = catalogTokens([]string{name, "--unknown", "output"}, nativeSpace, ReadOnlyCommandPolicy{})
		require.False(t, f.BoundedWrite, name)
	}
	for _, args := range [][]string{{"rm", "-rf", "output"}, {"rmdir", "-p", "output"}, {"touch", "-r"}, {"unlink", "a", "b"}} {
		require.False(t, catalogTokens(args, nativeSpace, ReadOnlyCommandPolicy{}).BoundedWrite, args)
	}
}
func TestStage5TransferProof(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("host file identity not implemented")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	source := filepath.Join(root, "source")
	require.NoError(t, os.WriteFile(source, []byte("unchanged"), 0600))
	dir := filepath.Join(root, "dest")
	require.NoError(t, os.Mkdir(dir, 0700))
	link := filepath.Join(root, "link")
	require.NoError(t, os.Symlink(source, link))
	hard := filepath.Join(root, "hard")
	require.NoError(t, os.WriteFile(hard, []byte("hard"), 0600))
	require.NoError(t, os.Link(hard, filepath.Join(root, "hard2")))
	for _, name := range []string{"cp", "mv"} {
		for _, tc := range []struct {
			args     []string
			complete bool
			target   string
		}{
			{[]string{source, filepath.Join(dir, "new")}, true, filepath.Join(dir, "new")},
			{[]string{"--", source, dir}, true, filepath.Join(dir, "source")},
			{[]string{source, source}, false, ""}, {[]string{link, dir}, false, ""},
			{[]string{source, link}, false, ""}, {[]string{hard, dir}, false, ""}, {[]string{source, hard}, false, ""},
			{[]string{dir, filepath.Join(root, "new")}, false, ""}, {[]string{source, filepath.Join(root, "missing", "new")}, false, ""},
			{[]string{"--backup", source, dir}, false, ""}, {[]string{"-r", source, dir}, false, ""},
			{[]string{source, dir + "/"}, false, ""}, {[]string{"dest/../source", dir}, false, ""},
		} {
			r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: name, Args: tc.args}})
			require.NoError(t, err)
			require.Equal(t, tc.complete, r.WriteCoverage == CoverageComplete, "%s %v", name, tc.args)
			if tc.complete {
				roles := map[string]TargetRole{}
				for _, target := range r.Targets {
					if target.Role != TargetCandidate {
						roles[target.Path] = target.Role
					}
				}
				require.Equal(t, TargetWrite, roles[tc.target])
				want := TargetRead
				if name == "mv" {
					want = TargetWrite
				}
				require.Equal(t, want, roles[source])
			}
		}
	}
	for _, command := range []string{"cp source dest; mystery", "cd dest; cp ../source new", "cp $INPUT dest", "cp source dest; touch $OUTPUT"} {
		in := shellInput(command)
		in.Cwd = root
		r, err := NewEngine(Settings{}, Dependencies{}).Analyze(context.Background(), in)
		require.NoError(t, err)
		require.Equal(t, CoverageIncomplete, r.WriteCoverage, command)
	}
	// Linux commonly exposes a distinct tmpfs here. Only inspect it; no writes.
	if info, statErr := os.Stat("/dev/shm"); statErr == nil && info.IsDir() {
		srcInfo, statErr := os.Stat(source)
		require.NoError(t, statErr)
		sourceDev, _, sourceOK := transferFileIdentity(srcInfo)
		otherDev, _, otherOK := transferFileIdentity(info)
		if sourceOK && otherOK && sourceDev != otherDev {
			_, proofErr := (hostPaths{}).ResolveTransfer(context.Background(), source, "/dev/shm")
			require.Error(t, proofErr, "cross-volume transfers are outside this proof")
		}
	}
	data, err := os.ReadFile(source)
	require.NoError(t, err)
	require.Equal(t, "unchanged", string(data))
	_, err = os.Stat(filepath.Join(dir, "source"))
	require.True(t, os.IsNotExist(err), "analysis must never execute transfers")
}
func TestStage5GitPredecessor(t *testing.T) {
	for _, tc := range []struct {
		source   string
		resolved bool
	}{
		{"cat input; git -C . push origin", true}, {"uniq -f2 input; git push origin", true},
		{"cd .; git push origin", false}, {"printf -v GIT_CONFIG file; git push origin", false}, {"mystery; git push origin", false},
		{"cat input > .git/config; git push origin", false},
		{"X=1 cat input; git push origin", false}, {"cat $(mystery); git push origin", false},
	} {
		calls := 0
		e := NewEngine(Settings{}, Dependencies{Git: gitFunc(func(_ context.Context, q GitQuery) (string, error) {
			if q.Kind == GitExecutable {
				return "", nil
			}
			calls++
			return "https://example.com/repo", nil
		})})
		r, err := e.Analyze(context.Background(), shellInput(tc.source))
		require.NoError(t, err)
		if tc.resolved {
			require.Contains(t, r.RemoteOrigins, "https://example.com", "%s: %+v", tc.source, r)
			require.Positive(t, calls)
		} else {
			require.NotEmpty(t, r.UnresolvedRemoteTargets, tc.source)
		}
		require.NotEqual(t, CoverageComplete, r.WriteCoverage, "known origins cannot prove hook/helper effects")
	}
}

func TestStage5TransferProofIsFreshAndOptional(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("host file identity not implemented")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	source, dest := filepath.Join(root, "source"), filepath.Join(root, "dest")
	require.NoError(t, os.WriteFile(source, []byte("data"), 0600))
	in := Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "cp", Args: []string{source, dest}}}
	e := NewEngine(Settings{}, Dependencies{})
	r, err := e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CoverageComplete, r.WriteCoverage)
	require.NoError(t, os.Symlink(source, dest))
	r, err = e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CoverageIncomplete, r.WriteCoverage, "a stale transfer proof must never survive revalidation")
	require.NoError(t, os.Remove(dest))
	e = NewEngine(Settings{}, Dependencies{Paths: pathsFunc{}})
	r, err = e.Analyze(context.Background(), in)
	require.NoError(t, err)
	require.Equal(t, CoverageIncomplete, r.WriteCoverage, "ordinary path resolution cannot substitute for file-type proof")
}

func TestStage5PowerShellRedirectionRemainsIncomplete(t *testing.T) {
	// Synthetic IR only: do not claim a real Windows host validation or expand
	// cmdlet bindings until scalar/array/provider forms are verified there.
	ir := &psBridgeIR{Commands: []string{"Set-Content"}, Invocations: []psCommandInvocation{{NodeID: 1, Name: "Set-Content", Args: []string{"-LiteralPath", `'C:\output\file'`, "-Value", "text", "-Encoding", "utf8"}}}}
	r := assessPowerShellIR("synthetic", ir, ReadOnlyCommandPolicy{}, "")
	require.Equal(t, EffectWrite, r.Effect)
	require.Equal(t, CoverageComplete, r.WriteCoverage)
	require.Len(t, r.Targets, 1)
	require.Equal(t, TargetWrite, r.Targets[0].Role)
	ir.Redirects = []string{"FileRedirection"}
	r = assessPowerShellIR("synthetic", ir, ReadOnlyCommandPolicy{}, "")
	require.Equal(t, CoverageIncomplete, r.WriteCoverage)
	require.Contains(t, r.Gaps, GapWriteTargets)
	ir.Redirects = nil
	ir.HasStopParsing = true
	r = assessPowerShellIR("synthetic", ir, ReadOnlyCommandPolicy{}, "")
	require.Equal(t, CoverageIncomplete, r.WriteCoverage)
}
