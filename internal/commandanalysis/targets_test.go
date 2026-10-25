package commandanalysis

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/stretchr/testify/require"
)

func TestRoleTargetsKeepSourcesAndDestinationSeparate(t *testing.T) {
	root := t.TempDir()
	e := NewEngine(Settings{WorkingDir: root}, Dependencies{})
	for _, program := range []string{"uniq", "xxd", "cp", "mv"} {
		r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: program, Args: []string{"input/source", "output/dest"}}})
		require.NoError(t, err)
		roles := map[string]TargetRole{}
		for _, target := range r.Targets {
			if target.Role != TargetCandidate {
				roles[target.Raw] = target.Role
			}
		}
		sourceRole := TargetRead
		if program == "mv" {
			sourceRole = TargetWrite
		}
		require.Equal(t, sourceRole, roles["input/source"])
		require.Equal(t, TargetWrite, roles["output/dest"])
		if program == "cp" || program == "mv" {
			require.Equal(t, CoverageIncomplete, r.WriteCoverage)
		} else {
			require.Equal(t, CoverageComplete, r.WriteCoverage)
		}
	}
}

func TestTargetOperationResolvesLeafOrParent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file symlink requires Windows privilege; directory junction covered in app tests")
	}
	root := t.TempDir()
	inside, outside := filepath.Join(root, "inside"), filepath.Join(root, "outside")
	require.NoError(t, os.MkdirAll(inside, 0700))
	require.NoError(t, os.MkdirAll(outside, 0700))
	dest := filepath.Join(outside, "real")
	require.NoError(t, os.WriteFile(dest, []byte("unchanged"), 0600))
	link := filepath.Join(inside, "link")
	require.NoError(t, os.Symlink(dest, link))
	e := NewEngine(Settings{WorkingDir: root}, Dependencies{})
	for _, program := range []string{"touch", "tee", "rm", "unlink", "rmdir", "mkdir"} {
		r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: program, Args: []string{link}}})
		require.NoError(t, err)
		var writes []Target
		for _, target := range r.Targets {
			if target.Role == TargetWrite {
				writes = append(writes, target)
			}
		}
		require.Len(t, writes, 1)
		if program == "touch" || program == "tee" {
			require.Equal(t, dest, writes[0].Path)
			require.Equal(t, outside, writes[0].Dir)
		} else {
			require.Equal(t, link, writes[0].Path)
			require.Equal(t, inside, writes[0].Dir)
		}
	}
	// Nonexistent leaf below a directory link still follows its existing parent.
	dirlink := filepath.Join(inside, "dirlink")
	require.NoError(t, os.Symlink(outside, dirlink))
	r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "touch", Args: []string{filepath.Join(dirlink, "new")}}})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(outside, "new"), r.Targets[0].Path)
	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "unchanged", string(content), "analysis must not execute writes")
}

func TestTargetLiteralAndRuntimeEvidence(t *testing.T) {
	root := t.TempDir()
	e := NewEngine(Settings{}, Dependencies{Environment: func() []string { return []string{"HOME=" + root} }})
	for _, tc := range []struct {
		source   string
		literal  bool
		complete bool
	}{
		{`touch '$HOME/file'`, true, false}, // precise path, legacy grammar remains conservative
		{`touch "$HOME/file"`, false, true},
		{`rm *`, false, false},
		{`cat "$MISSING/input" > output`, false, true},
	} {
		r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: tc.source}})
		require.NoError(t, err)
		require.Equal(t, tc.complete, r.WriteCoverage == CoverageComplete, tc.source)
		if tc.literal {
			require.Equal(t, filepath.Join(root, "$HOME", "file"), r.Targets[0].Path)
		}
	}
	r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "touch", Args: []string{"$HOME/file"}}})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "$HOME", "file"), r.Targets[0].Path)
	// A quoted literal output with spaces keeps the complete leaf until resolution.
	r, err = e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Shell: &ShellInput{Source: "touch " + strconv.Quote("space name")}})
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "space name"), r.Targets[0].Path)
}

func TestCreationIncludesMissingAncestorEntries(t *testing.T) {
	root := t.TempDir()
	e := NewEngine(Settings{}, Dependencies{})
	r, err := e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "mkdir", Args: []string{"-p", filepath.Join(root, "new", "nested")}}})
	require.NoError(t, err)
	require.Equal(t, CoverageComplete, r.WriteCoverage)
	require.Equal(t, root, r.Targets[0].Dir, "mkdir -p changes the existing ancestor to create new")
	for _, arg := range []string{"link/", "link/.", "link/.."} {
		r, err = e.Analyze(context.Background(), Input{Cwd: root, Dialect: pathutil.FlavorPOSIX, Process: &ProcessInput{Program: "rm", Args: []string{"-rf", arg}}})
		require.NoError(t, err)
		require.Equal(t, CoverageIncomplete, r.WriteCoverage, arg)
	}
}

func TestPowerShellRoleMetadataPreservesLiteralSource(t *testing.T) {
	ir := &psBridgeIR{Invocations: []psCommandInvocation{
		{NodeID: 1, Name: "Get-Content", Args: []string{"-LiteralPath", `'C:\input\source'`}},
		{NodeID: 2, Name: "Set-Content", Args: []string{"-LiteralPath", `'C:\output\$literal'`, "-Value", "text"}},
	}}
	result := assessPowerShellIR("synthetic", ir, ReadOnlyCommandPolicy{}, "")
	require.Len(t, result.Targets, 2)
	require.Equal(t, TargetRead, result.Targets[0].Role)
	require.Equal(t, TargetWrite, result.Targets[1].Role)
	require.True(t, result.Targets[1].Literal)
	require.Equal(t, 2, result.Targets[1].NodeID)
}

func TestTrustedPowerShellGitDoesNotAcquireWriteRoles(t *testing.T) {
	root := t.TempDir()
	run := NewEngine(Settings{}, Dependencies{Environment: func() []string { return nil }, Paths: pathsFunc{}}).run(context.Background())
	ir := &psBridgeIR{Commands: []string{"git"}, Invocations: []psCommandInvocation{{NodeID: 1, Name: "git", Args: []string{"add", "file"}}}}
	r := assessPowerShellIRContext("git add file", ir, ReadOnlyCommandPolicy{Commands: []string{"git"}}, ArgvStaticContext{Cwd: root, run: run})
	require.Equal(t, EffectRead, r.Effect)
	for _, target := range r.Targets {
		require.NotEqual(t, TargetWrite, target.Role)
	}
}
