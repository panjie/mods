package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestShellAssessmentPWDUsesExecutionDirectory(t *testing.T) {
	parent, child := t.TempDir(), t.TempDir()
	var err error
	parent, err = filepath.EvalSymlinks(parent)
	require.NoError(t, err)
	child, err = filepath.EvalSymlinks(child)
	require.NoError(t, err)
	t.Setenv("PWD", parent)
	m := &Mods{Config: testConfigForWorkspace(parent), shellAnalyzer: func(string, string) approval.CommandAssessment {
		return approval.UnknownCommandAssessment()
	}}
	for _, command := range []string{`touch "$PWD/probe.txt"`, `touch "${PWD}/probe.txt"`} {
		a := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, command, nil, child)
		intent := normalizeAccessIntentDirs(a.AccessIntent(), child, "shell_run", true)
		require.Empty(t, a.DynamicTargets)
		require.Equal(t, slashSet([]string{filepath.Join(child, "probe.txt")}), slashSet(a.KnownDirs))
		for _, tc := range []struct {
			dir     string
			allowed bool
		}{{parent, false}, {child, true}} {
			rules := approval.RulesForDirs([]string{tc.dir}, WorkspaceScope(parent), AccessWrite)
			require.Equal(t, tc.allowed, approval.RulesAllowIntent(rules, intent, WorkspaceScope(parent), nil, approval.ReviewAuto))
		}
	}
	for _, tc := range []struct {
		command, cwd string
		shadow       map[string]bool
	}{
		{`touch "$PWD/probe.txt"`, "", nil},
		{`touch "$PWD/probe.txt"`, child, map[string]bool{"PWD": true}},
		{`cd /; touch "$PWD/probe.txt"`, child, nil},
		{`PWD=/; touch "$PWD/probe.txt"`, child, nil},
	} {
		a := m.assessShellCommand("shell_run", pathutil.FlavorPOSIX, tc.command, tc.shadow, tc.cwd)
		require.NotEmpty(t, a.DynamicTargets, "unreliable PWD must remain dynamic: %s", tc.command)
	}
	if runtime.GOOS == "windows" {
		return // The POSIX assessment above is portable; shell_run on Windows is not sh.
	}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(registry, toolregistry.ShellConfig{Root: parent}))
	data, err := json.Marshal(map[string]string{"command": `touch "$PWD/probe.txt"`, "cwd": child})
	require.NoError(t, err)
	_, err = registry.Call(context.Background(), "shell_run", data)
	require.NoError(t, err)
	require.FileExists(t, filepath.Join(child, "probe.txt"))
	require.NoFileExists(t, filepath.Join(parent, "probe.txt"))
}

func TestDownloadToolCallerUsesOwnTimeoutAndHonorsCancellation(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded"))
	}))
	defer server.Close()
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = t.TempDir()
	cfg.ReviewMode = ReviewNever
	cfg.MCPTimeout = time.Nanosecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterDownload(registry, toolregistry.FilesystemConfig{Root: cfg.BuiltinTools.Workspace}))
	data, err := json.Marshal(map[string]any{"files": []map[string]string{{"url": server.URL, "path": "test.txt"}}})
	require.NoError(t, err)
	caller := m.toolCaller(registry, &cfg)
	out, err := caller(proto.ToolCallRequest{Name: "http_download", Arguments: data})
	require.NoError(t, err)
	require.Contains(t, out, `"completed"`)
	content, err := os.ReadFile(filepath.Join(cfg.BuiltinTools.Workspace, "test.txt"))
	require.NoError(t, err)
	require.Equal(t, "downloaded", string(content))
	cancel()
	data, err = json.Marshal(map[string]any{"files": []map[string]string{{"url": server.URL, "path": "canceled.txt"}}})
	require.NoError(t, err)
	_, err = caller(proto.ToolCallRequest{Name: "http_download", Arguments: data})
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(cfg.BuiltinTools.Workspace, "canceled.txt"))
}
