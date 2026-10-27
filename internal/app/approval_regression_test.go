package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestApprovalP1WritableReadForms(t *testing.T) {
	for _, command := range []string{"uniq input.txt output.txt", "xxd input.txt output.txt", "git reflog expire --expire=now --all"} {
		t.Run(command, func(t *testing.T) {
			a := commandanalysis.AssessShellStaticWithContext(command, true, commandanalysis.ReadOnlyCommandPolicy{}, "/workspace")
			require.NotEqual(t, commandanalysis.EffectRead, a.Effect)
			require.False(t, a.StaticRead)
		})
	}
	for _, program := range []string{"uniq", "xxd"} {
		for _, posix := range []bool{true, false} {
			a := commandanalysis.AssessArgvStaticWithPolicy(program, []string{"input.txt", "output.txt"}, posix, commandanalysis.ReadOnlyCommandPolicy{})
			require.NotEqual(t, commandanalysis.EffectRead, a.Effect)
		}
	}
}

func TestPreparedNativeWriteReapprovesChangedTarget(t *testing.T) {
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	require.NoError(t, os.Mkdir(first, 0o700))
	require.NoError(t, os.Mkdir(second, 0o700))
	link := filepath.Join(root, "link")
	approvalTestDirectoryLink(t, first, link)
	defer os.Remove(link)
	cfg := defaultConfig()
	cfg.WorkingDir = root
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(root), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{Root: root}))
	args, err := json.Marshal(map[string]string{"path": filepath.Join(link, "result.txt"), "content": "approved"})
	require.NoError(t, err)
	done := make(chan error, 1)
	go func() {
		_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "fs_write_file", Arguments: args})
		done <- err
	}()
	item := receiveReviewItem(t, r.reviewChan)
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Write scope", Value: first})
	require.NoError(t, os.Remove(link))
	approvalTestDirectoryLink(t, second, link)
	item.resp <- reviewResponse{action: reviewActionApprove}
	item = receiveReviewItem(t, r.reviewChan)
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Write scope", Value: second})
	_, err = os.Stat(filepath.Join(second, "result.txt"))
	require.True(t, os.IsNotExist(err), "changed target must wait for fresh approval")
	item.resp <- reviewResponse{action: reviewActionApprove}
	require.NoError(t, <-done)
	_, err = os.Stat(filepath.Join(first, "result.txt"))
	require.True(t, os.IsNotExist(err))
	content, err := os.ReadFile(filepath.Join(second, "result.txt"))
	require.NoError(t, err)
	require.Equal(t, "approved", string(content))
}

func TestApprovalP1UnknownEffectsCannotBorrowTargets(t *testing.T) {
	for _, tc := range []struct {
		command, cwd, safe, tool string
		flavor                   pathutil.Flavor
	}{
		{"touch /scratch/marker; unknown-writer", "/workspace", "/scratch", "posix", pathutil.FlavorPOSIX},
		{"unknown-writer > /scratch/log", "/workspace", "/scratch", "posix", pathutil.FlavorPOSIX},
		{`Set-Content C:\scratch\marker.txt ok; unknown-writer`, `C:\workspace`, `C:\scratch`, "powershell_run", pathutil.FlavorPowerShell},
	} {
		t.Run(tc.command, func(t *testing.T) {
			m := &Mods{Config: &Config{}}
			a := m.assessShellCommand(tc.tool, tc.flavor, tc.command, nil, tc.cwd)
			intent := normalizeAccessIntentDirs(commandResultToIntent(a), tc.cwd, tc.tool, true)
			scope := approval.WorkingDirScope(tc.cwd)
			for _, mode := range []approval.ReviewMode{approval.ReviewAuto, approval.ReviewAlways} {
				require.Equal(t, approval.DecisionAsk, approval.ClassifyAccess(intent, scope, []string{tc.safe}, mode))
				require.False(t, approval.RulesAllowIntent(approval.RulesForDirs([]string{tc.safe}, scope, approval.AccessWrite), intent, scope, nil, mode))
			}
			require.Empty(t, candidateRulesForIntent(intent, scope, []string{tc.safe}, approval.ReviewAuto))
		})
	}
}

func TestApprovalP1GitPushTargets(t *testing.T) {
	repo := t.TempDir()
	gitFixture := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	gitFixture("init", "-q")
	gitFixture("remote", "add", "origin", "https://first.example/repo.git")
	gitFixture("config", "--add", "remote.origin.pushurl", "https://first.example/repo.git")
	gitFixture("config", "--add", "remote.origin.pushurl", "https://second.example/repo.git")
	gitFixture("remote", "add", "other", "https://third.example/repo.git")
	gitFixture("config", "remote.pushDefault", "other")
	m := &Mods{ctx: context.Background(), Config: testConfigForWorkingDir(repo)}
	for _, tc := range []struct {
		command string
		want    []string
	}{
		{"git push origin", []string{"https://first.example", "https://second.example"}},
		{"git push", []string{"https://third.example"}},
		{"git push origin; git push other", []string{"https://first.example", "https://second.example", "https://third.example"}},
		{"git push --repo other", []string{"https://third.example"}},
		{"git push --repo=other", []string{"https://third.example"}},
	} {
		t.Run(tc.command, func(t *testing.T) {
			origins, unresolved, _ := m.resolveGitPushOrigins("shell_run", tc.command, repo)
			require.Empty(t, unresolved)
			require.ElementsMatch(t, tc.want, origins)
		})
	}
	t.Run("local remote is a directory", func(t *testing.T) {
		local := t.TempDir()
		gitFixture("remote", "add", "local", local)
		origins, unresolved, dirs := m.resolveGitPushOrigins("shell_run", "git push local", repo)
		require.Empty(t, origins)
		require.Empty(t, unresolved)
		canonical, err := pathutil.ResolveThroughExistingParent(local)
		require.NoError(t, err)
		require.Equal(t, []string{canonical}, dirs)
	})
	t.Run("push URL rewrite", func(t *testing.T) {
		gitFixture("remote", "add", "rewritten", "https://before.example/repo")
		gitFixture("config", "url.https://after.example/.pushInsteadOf", "https://before.example/")
		origins, unresolved, _ := m.resolveGitPushOrigins("shell_run", "git push rewritten", repo)
		require.Empty(t, unresolved)
		require.Equal(t, []string{"https://after.example"}, origins)
		origins, unresolved, _ = m.resolveGitPushOrigins("shell_run", "git push https://before.example/repo", repo)
		require.Empty(t, unresolved)
		require.Equal(t, []string{"https://after.example"}, origins)
	})
	t.Run("longest push prefix is not rewritten recursively", func(t *testing.T) {
		gitFixture("config", "url.https://longest.example/.pushInsteadOf", "https://before.example/team/")
		gitFixture("config", "url.https://final.example/.insteadOf", "https://longest.example/")
		gitFixture("remote", "add", "combined", "https://before.example/team/repo")
		aliasOrigins, unresolved, _ := m.resolveGitPushOrigins("shell_run", "git push combined", repo)
		require.Empty(t, unresolved)
		require.Equal(t, []string{"https://longest.example"}, aliasOrigins)
		directOrigins, unresolved, _ := m.resolveGitPushOrigins("shell_run", "git push https://before.example/team/repo", repo)
		require.Empty(t, unresolved)
		require.Equal(t, aliasOrigins, directOrigins)
	})
	t.Run("insteadOf fallback matches Git", func(t *testing.T) {
		gitFixture("config", "url.https://final.example/.insteadOf", "https://longest.example/")
		gitFixture("remote", "add", "fallback", "https://longest.example/repo")
		aliasOrigins, unresolved, _ := m.resolveGitPushOrigins("shell_run", "git push fallback", repo)
		require.Empty(t, unresolved)
		require.Equal(t, []string{"https://final.example"}, aliasOrigins)
		directOrigins, unresolved, _ := m.resolveGitPushOrigins("shell_run", "git push https://longest.example/repo", repo)
		require.Empty(t, unresolved)
		require.Equal(t, aliasOrigins, directOrigins)
	})
	t.Run("unknown helper stays unresolved", func(t *testing.T) {
		gitFixture("remote", "add", "helper", "ext::untrusted-helper")
		origins, unresolved, dirs := m.resolveGitPushOrigins("shell_run", "git push helper", repo)
		require.Empty(t, origins)
		require.Empty(t, dirs)
		require.NotEmpty(t, unresolved)
	})
}

func TestGitTargetChangeRequiresFreshReview(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		require.NoError(t, err, "%s", out)
	}
	git("init", "-q")
	git("remote", "add", "origin", "https://before.example/repo")
	cfg := defaultConfig()
	cfg.WorkingDir = repo
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	r := &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(repo), reviewAvailabilityKnown: true, interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1)}
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: r}
	registry := toolregistry.NewRegistry()
	called := false
	require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { called = true; return "ok", nil }}))
	done := make(chan error, 1)
	go func() {
		_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: []byte(`{"command":"git push origin"}`)})
		done <- err
	}()
	item := receiveReviewItem(t, r.reviewChan)
	require.Empty(t, item.candidateRules, "hooks and helpers remain unbounded")
	git("remote", "set-url", "origin", "https://after.example/repo")
	item.resp <- reviewResponse{action: reviewActionApprove}
	item = receiveReviewItem(t, r.reviewChan)
	require.Contains(t, item.presentation.rows, interactionRow{Label: "Write target", Value: "https://after.example"})
	item.resp <- reviewResponse{action: reviewActionDeny}
	require.Error(t, <-done)
	require.False(t, called, "denied refreshed target must never dispatch")
}

func approvalTestDirectoryLink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err == nil {
		return
	}
	if runtime.GOOS != "windows" {
		t.Skip("symlinks unavailable")
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:MODS_TEST_LINK -Target $env:MODS_TEST_TARGET -ErrorAction Stop | Out-Null`)
	cmd.Env = append(os.Environ(), "MODS_TEST_LINK="+link, "MODS_TEST_TARGET="+target)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
}

func TestApprovalP1DirectoryLinkCannotExpandSavedRule(t *testing.T) {
	root := t.TempDir()
	allowed, other := filepath.Join(root, "allowed"), filepath.Join(root, "other")
	require.NoError(t, os.Mkdir(allowed, 0o700))
	require.NoError(t, os.Mkdir(other, 0o700))
	link := filepath.Join(allowed, "link")
	approvalTestDirectoryLink(t, other, link)
	defer os.Remove(link)
	cfg := defaultConfig()
	cfg.WorkingDir, cfg.ReviewMode, cfg.InteractiveTTYAvailable = root, ReviewAuto, false
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	m.reviewer.rules.Add(RulesForDirs([]string{allowed}, m.reviewer.scope, AccessWrite)...)
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{Root: root}))
	args, err := json.Marshal(map[string]string{"path": filepath.Join(link, "audit.txt"), "content": "audit"})
	require.NoError(t, err)
	_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "fs_write_file", Arguments: args})
	require.ErrorIs(t, err, errReviewUnavailable)
	_, err = os.Stat(filepath.Join(other, "audit.txt"))
	require.True(t, os.IsNotExist(err), "unapproved target must stay untouched")
	m.reviewer.rules.Add(RulesForDirs([]string{other}, m.reviewer.scope, AccessWrite)...)
	_, err = m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "fs_write_file", Arguments: args})
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(other, "audit.txt"))
	require.NoError(t, err)
	require.Equal(t, "audit", string(content))
}
