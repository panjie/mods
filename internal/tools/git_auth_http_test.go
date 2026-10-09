package tools

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/cgi"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Exercise real HTTP challenges and Git's actual backend, including a write.
// All repositories, credentials, certificates and network traffic are local.
func TestGitAuthHTTPSCommands(t *testing.T) {
	git := isolateGitAuth(t)
	root := t.TempDir()
	localGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, string(out))
		return strings.TrimSpace(string(out))
	}
	localGit("init", "--bare", "-q", "remote.git")
	localGit("-C", "remote.git", "config", "http.receivepack", "true")
	localGit("init", "-q", "source")
	localGit("-C", "source", "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-qm", "fixture")
	head := localGit("-C", "source", "rev-parse", "HEAD")
	backend := &cgi.Handler{Path: git, Args: []string{"http-backend"}, Env: []string{"GIT_PROJECT_ROOT=" + root, "GIT_HTTP_EXPORT_ALL=1", "REMOTE_USER=fixture-user"}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || user != "fixture-user" || password != "fixture-token" {
			w.Header().Set("WWW-Authenticate", `Basic realm="mods-test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		backend.ServeHTTP(w, r)
	}))
	defer server.Close()
	certPath := filepath.Join(root, "ca.pem")
	require.NoError(t, os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0o600))
	t.Setenv("GIT_SSL_CAINFO", certPath)
	var prompts atomic.Int32
	handler := func(_ context.Context, req GitAuthRequest) (string, error) {
		prompts.Add(1)
		if req.Username {
			return "fixture-user", nil
		}
		return "fixture-token", nil
	}
	url := server.URL + "/remote.git"
	for _, args := range [][]string{
		{"-C", "source", "push", url, "HEAD:refs/heads/main"},
		{"clone", "--branch", "main", url, "clone"},
		{"-C", "clone", "fetch", "origin"},
	} {
		out, err := runProcess(context.Background(), ProcessConfig{Root: root, Timeout: 15 * time.Second, GitAuth: handler}, root, processRunArgs{Program: "git", Args: args})
		require.NoError(t, err)
		var result processRunResult
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.NotNil(t, result.ExitCode, out)
		require.Zero(t, *result.ExitCode, out)
	}
	require.Equal(t, head, localGit("-C", "clone", "rev-parse", "HEAD"))
	require.GreaterOrEqual(t, prompts.Load(), int32(6))
	// Run a prewritten script through the platform's registered shell path.
	var command string
	build := shellCommand
	tool := "shell_run"
	if runtime.GOOS == "windows" {
		_, err := exec.LookPath(windowsPowerShellExe())
		require.NoError(t, err)
		script := filepath.Join(root, "fetch script.ps1")
		require.NoError(t, os.WriteFile(script, []byte("git ls-remote '"+url+"'\nexit $LASTEXITCODE\n"), 0o600))
		command = "& '" + strings.ReplaceAll(script, "'", "''") + "'"
		build = powerShellCommand
		tool = "powershell_run"
	} else {
		script := filepath.Join(root, "fetch script.sh")
		require.NoError(t, os.WriteFile(script, []byte("git ls-remote '"+url+"'\n"), 0o600))
		command = "sh '" + strings.ReplaceAll(script, "'", "'\\''") + "'"
	}
	out, err := runShellCommand(context.Background(), ShellConfig{Root: root, Timeout: 15 * time.Second, GitAuth: handler}, root, tool, command, nil, nil, build)
	require.NoError(t, err, out)
	require.Contains(t, out, head)
	// Discovery uses the very same configured registry but cannot open a UI.
	before := prompts.Load()
	_, err = runShellCommand(WithoutGitAuthInput(context.Background()), ShellConfig{Root: root, Timeout: 5 * time.Second, GitAuth: handler}, root, tool, command, nil, nil, build)
	require.Error(t, err)
	require.Equal(t, before, prompts.Load())
}
