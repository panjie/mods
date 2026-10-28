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

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

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
