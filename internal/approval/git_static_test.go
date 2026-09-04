package approval

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func makeStaticGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	return root
}

func TestAssessArgvStaticGitWorkspaceWrites(t *testing.T) {
	root := makeStaticGitRepo(t)
	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(filepath.Join(root, ".git"))
	require.NoError(t, err)
	wantDirs := dedupeSorted([]string{canonicalRoot, canonicalGitDir})
	for _, tc := range []struct {
		name string
		args []string
	}{
		{name: "add all", args: []string{"add", "-A"}},
		{name: "add paths", args: []string{"add", "internal/app/render.go"}},
		{name: "checkout path", args: []string{"checkout", "--", "internal/app/render.go"}},
		{name: "restore path", args: []string{"restore", "internal/app/render.go"}},
		{name: "reset", args: []string{"reset", "--hard"}},
		{name: "clean", args: []string{"clean", "-fd"}},
		{name: "remove", args: []string{"rm", "old.go"}},
		{name: "move", args: []string{"mv", "old.go", "new.go"}},
		{name: "commit with message", args: []string{"commit", "-m", "Confine scalar command substitutions"}},
		{name: "commit amend", args: []string{"commit", "--amend", "--no-edit"}},
		{name: "merge branch", args: []string{"merge", "feature"}},
		{name: "rebase upstream", args: []string{"rebase", "origin/main"}},
		{name: "cherry-pick commit", args: []string{"cherry-pick", "abc1234"}},
		{name: "revert commit", args: []string{"revert", "HEAD"}},
		{name: "stash push", args: []string{"stash", "push", "-m", "wip"}},
		{name: "stash pop", args: []string{"stash", "pop"}},
		{name: "switch branch", args: []string{"switch", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessArgvStaticWithContext("git", tc.args, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: root})
			require.Equal(t, EffectWrite, got.Effect)
			require.Equal(t, wantDirs, got.KnownDirs)
			require.Contains(t, got.Reason, "static argv analysis")
		})
	}

	if runtime.GOOS == "windows" {
		got := AssessArgvStaticWithContext("Git.EXE", []string{"add", "-A"}, false, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: root})
		require.Equal(t, EffectWrite, got.Effect)
		require.Equal(t, wantDirs, got.KnownDirs)
	}
}

func TestAssessArgvStaticGitWriteDiscoversRepositoryFromSubdirectory(t *testing.T) {
	root := makeStaticGitRepo(t)
	cwd := filepath.Join(root, "internal", "app")
	require.NoError(t, os.MkdirAll(cwd, 0o755))

	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(filepath.Join(root, ".git"))
	require.NoError(t, err)
	got := AssessArgvStaticWithContext("git", []string{"add", "-A"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: cwd})

	require.Equal(t, EffectWrite, got.Effect)
	require.Equal(t, dedupeSorted([]string{canonicalRoot, canonicalGitDir}), got.KnownDirs)
}

func TestAssessArgvStaticGitLinkedWorktreeIncludesExternalMetadata(t *testing.T) {
	root := t.TempDir()
	metadataRoot := t.TempDir()
	gitDir := filepath.Join(metadataRoot, "worktrees", "topic")
	commonDir := filepath.Join(metadataRoot, "common")
	require.NoError(t, os.MkdirAll(gitDir, 0o755))
	require.NoError(t, os.MkdirAll(commonDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(gitDir, "commondir"), []byte(filepath.Join("..", "..", "common")+"\n"), 0o600))

	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(gitDir)
	require.NoError(t, err)
	canonicalCommonDir, err := canonicalExistingDir(commonDir)
	require.NoError(t, err)
	got := AssessArgvStaticWithContext("git", []string{"checkout", "--", "file.go"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: root})

	require.Equal(t, EffectWrite, got.Effect)
	require.Equal(t, dedupeSorted([]string{canonicalRoot, canonicalGitDir, canonicalCommonDir}), got.KnownDirs)
}

func TestAssessArgvStaticGitWriteFailsClosed(t *testing.T) {
	root := makeStaticGitRepo(t)

	for _, tc := range []struct {
		name    string
		program string
		args    []string
		context ArgvStaticContext
	}{
		{name: "missing cwd context", program: "git", args: []string{"add", "-A"}},
		{name: "global repository selector", program: "git", args: []string{"-C", root, "add", "-A"}, context: ArgvStaticContext{Cwd: root}},
		{name: "pathspec indirection", program: "git", args: []string{"add", "--pathspec-from-file=paths.txt"}, context: ArgvStaticContext{Cwd: root}},
		{name: "help may launch viewer", program: "git", args: []string{"checkout", "--help"}, context: ArgvStaticContext{Cwd: root}},
		{name: "invocation environment redirect", program: "git", args: []string{"add", "-A"}, context: ArgvStaticContext{Cwd: root, EnvironmentKeys: []string{"GIT_INDEX_FILE"}}},
		{name: "unknown git command", program: "git", args: []string{"remote", "add", "origin", "https://example.com/repo.git"}, context: ArgvStaticContext{Cwd: root}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessArgvStaticWithContext(tc.program, tc.args, true, ReadOnlyCommandPolicy{}, tc.context)
			require.Equal(t, EffectUnknown, got.Effect)
			require.Empty(t, got.KnownDirs)
		})
	}

	withoutRepo := t.TempDir()
	got := AssessArgvStaticWithContext("git", []string{"add", "-A"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: withoutRepo})
	require.Equal(t, EffectUnknown, got.Effect)
}

func TestAssessArgvStaticGitWriteHonorsInheritedRepositoryEnvironment(t *testing.T) {
	root := makeStaticGitRepo(t)
	t.Setenv("GIT_WORK_TREE", t.TempDir())

	got := AssessArgvStaticWithContext("git", []string{"add", "-A"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: root})

	require.Equal(t, EffectUnknown, got.Effect)
	require.Empty(t, got.KnownDirs)
}

func TestAssessArgvStaticGitMalformedMetadataFailsClosed(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, ".git"), []byte("not a gitdir pointer\n"), 0o600))

	got := AssessArgvStaticWithContext("git", []string{"add", "-A"}, true, ReadOnlyCommandPolicy{}, ArgvStaticContext{Cwd: root})

	require.Equal(t, EffectUnknown, got.Effect)
	require.Empty(t, got.KnownDirs)
}

func TestAssessShellStaticGitWorkspaceWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell_run uses PowerShell on Windows; POSIX AST coverage applies to non-Windows shell_run")
	}
	root := makeStaticGitRepo(t)
	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(filepath.Join(root, ".git"))
	require.NoError(t, err)
	wantDirs := dedupeSorted([]string{canonicalRoot, canonicalGitDir})

	cases := []struct {
		name    string
		command string
	}{
		{name: "commit with quoted message", command: `git commit -m "Confine scalar command substitutions and widen read-only detection"`},
		{name: "commit amend", command: `git commit --amend --no-edit`},
		{name: "stage then commit", command: `git add . && git commit -m "widen read-only detection"`},
		{name: "merge", command: `git merge feature`},
		{name: "stash push", command: `git stash push -m "wip"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := AssessShellStaticWithContext(tc.command, true, ReadOnlyCommandPolicy{}, root)
			require.Equal(t, EffectWrite, got.Effect)
			require.Equal(t, wantDirs, got.KnownDirs)
			require.Contains(t, got.Reason, "static")
		})
	}

	t.Run("empty cwd keeps git writes unresolved", func(t *testing.T) {
		got := AssessShellStaticWithPolicy(`git commit -m "msg"`, true, ReadOnlyCommandPolicy{})
		require.Equal(t, EffectUnknown, got.Effect)
		require.Empty(t, got.KnownDirs)
	})

	t.Run("non-repository cwd fails closed", func(t *testing.T) {
		got := AssessShellStaticWithContext(`git commit -m "msg"`, true, ReadOnlyCommandPolicy{}, t.TempDir())
		require.Equal(t, EffectUnknown, got.Effect)
		require.Empty(t, got.KnownDirs)
	})

	t.Run("global selector stays rejected", func(t *testing.T) {
		got := AssessShellStaticWithContext(`git -C `+root+` commit -m "msg"`, true, ReadOnlyCommandPolicy{}, root)
		require.Equal(t, EffectUnknown, got.Effect)
		require.Empty(t, got.KnownDirs)
	})

	t.Run("listing subcommands stay unwritten", func(t *testing.T) {
		for _, command := range []string{`git tag`, `git branch`} {
			got := AssessShellStaticWithContext(command, true, ReadOnlyCommandPolicy{}, root)
			require.NotEqual(t, EffectWrite, got.Effect, command)
		}
	})
}

func TestExtractWritableDirsWithCwdResolvesGitRepository(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX AST coverage applies to non-Windows shell_run")
	}
	root := makeStaticGitRepo(t)
	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(filepath.Join(root, ".git"))
	require.NoError(t, err)

	require.Equal(t, dedupeSorted([]string{canonicalRoot, canonicalGitDir}),
		ExtractWritableDirsWithCwd(`git commit -m "msg"`, true, root))
	require.Empty(t, ExtractWritableDirsWithCwd(`git commit -m "msg"`, true, ""))
}

func TestAnalyzePowerShellWritablePathsIRGitCommitResolvesRepository(t *testing.T) {
	root := makeStaticGitRepo(t)
	canonicalRoot, err := canonicalExistingDir(root)
	require.NoError(t, err)
	canonicalGitDir, err := canonicalExistingDir(filepath.Join(root, ".git"))
	require.NoError(t, err)

	ir := &psBridgeIR{
		Commands:               []string{"git"},
		Invocations:            []psCommandInvocation{{Name: "git", Args: []string{"commit", "-m", "widen read-only detection"}}},
		TopLevelStatementCount: 1,
		PipelineCount:          1,
	}
	dirs, _, known := analyzePowerShellWritablePathsIR(ir, ReadOnlyCommandPolicy{}, root)
	require.True(t, known)
	require.Equal(t, dedupeSorted([]string{canonicalRoot, canonicalGitDir}), dirs)

	_, _, knownWithoutCwd := analyzePowerShellWritablePathsIR(ir, ReadOnlyCommandPolicy{}, "")
	require.False(t, knownWithoutCwd)
}
