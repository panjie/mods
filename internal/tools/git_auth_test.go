package tools

import (
	"context"
	"encoding/json"
	"errors"
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

func isolateGitAuth(t *testing.T) string {
	t.Helper()
	git, err := exec.LookPath("git")
	require.NoError(t, err, "Git is required for authentication integration tests")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	for _, key := range []string{"GIT_ASKPASS", "SSH_ASKPASS", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT", "GIT_DIR", "GIT_WORK_TREE", "GIT_TRACE", "GIT_TRACE_CURL", "GIT_TRACE2", "GIT_TRACE2_EVENT", "GIT_TRACE2_PERF"} {
		t.Setenv(key, "")
		require.NoError(t, os.Unsetenv(key))
	}
	t.Setenv("LC_ALL", "C")
	return git
}

func gitCredentialFill(t *testing.T, git string, options []string, prompt GitAuthHandler) (string, string, error) {
	t.Helper()
	stdout, stderr := newBoundedOutput(4096), newBoundedOutput(4096)
	result, err := (commandRunner{
		Timeout: 5 * time.Second,
		BuildCommand: func(ctx context.Context) *exec.Cmd {
			cmd := exec.CommandContext(ctx, git, append(options, "credential", "fill")...)
			cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
			return cmd
		},
		Prepare: func(ctx context.Context, cmd *exec.Cmd) (func(), error) {
			return prepareGitAuth(ctx, cmd, "git credential fill", prompt)
		},
		Stdout: stdout, Stderr: stderr,
	}).Run()
	if err == nil {
		err = result.WaitError
	}
	return stdout.Snapshot().Text, stderr.Snapshot().Text, err
}

func writeGitTestAskpass(t *testing.T, name, value string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+" askpass.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\nprintf '%s\\n' '"+value+"'\n"), 0o700))
	return path
}

func TestGitAuthCredentialPrecedence(t *testing.T) {
	git := isolateGitAuth(t)
	var calls atomic.Int32
	prompt := func(_ context.Context, req GitAuthRequest) (string, error) {
		calls.Add(1)
		if req.Username {
			return "bridge-user", nil
		}
		return "tøken $'\\with-spaces", nil
	}
	fallback := writeGitTestAskpass(t, "SSH", "ssh-configured")
	explicit := writeGitTestAskpass(t, "Git", "git-configured")
	core := writeGitTestAskpass(t, "Core", "core-configured")
	for _, tc := range []struct {
		name, gitAsk, sshAsk, want string
		options                    []string
		calls                      int32
	}{
		{name: "bridge", want: "password=tøken $'\\with-spaces", calls: 2},
		{name: "core", options: []string{"-c", "core.askPass=" + core}, want: "password=core-configured"},
		{name: "ssh", sshAsk: fallback, want: "password=ssh-configured"},
		{name: "core before ssh", sshAsk: fallback, options: []string{"-c", "core.askPass=" + core}, want: "password=core-configured"},
		{name: "git before core", gitAsk: explicit, options: []string{"-c", "core.askPass=" + core}, want: "password=git-configured"},
		{name: "cache before askpass", gitAsk: explicit, options: []string{"-c", "credential.helper=!f() { echo username=cached; echo password=cached; }; f"}, want: "password=cached"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_ASKPASS", tc.gitAsk)
			t.Setenv("SSH_ASKPASS", tc.sshAsk)
			if tc.gitAsk == "" {
				require.NoError(t, os.Unsetenv("GIT_ASKPASS"))
			}
			if tc.sshAsk == "" {
				require.NoError(t, os.Unsetenv("SSH_ASKPASS"))
			}
			calls.Store(0)
			out, stderr, err := gitCredentialFill(t, git, tc.options, prompt)
			require.NoError(t, err, stderr)
			require.Contains(t, out, tc.want)
			require.Equal(t, tc.calls, calls.Load())
		})
	}
	t.Run("configured helper failure does not fall back", func(t *testing.T) {
		calls.Store(0)
		_, stderr, err := gitCredentialFill(t, git, []string{"-c", "core.askPass=" + filepath.Join(t.TempDir(), "missing")}, prompt)
		require.Error(t, err)
		require.Contains(t, stderr, "terminal prompts disabled")
		require.Zero(t, calls.Load())
	})
}

func TestGitAuthRepositoryConfigAndNonInteractive(t *testing.T) {
	git := isolateGitAuth(t)
	repo := t.TempDir()
	out, err := exec.Command(git, "init", "-q", repo).CombinedOutput()
	require.NoError(t, err, string(out))
	helper := writeGitTestAskpass(t, "Local", "local-configured")
	out, err = exec.Command(git, "-C", repo, "config", "core.askPass", helper).CombinedOutput()
	require.NoError(t, err, string(out))
	for _, prompt := range []GitAuthHandler{nil, func(context.Context, GitAuthRequest) (string, error) {
		t.Error("local askpass must win")
		return "", nil
	}} {
		out, stderr, err := gitCredentialFill(t, git, []string{"-C", repo}, prompt)
		require.NoError(t, err, stderr)
		require.Contains(t, out, "password=local-configured")
	}
	_, stderr, err := gitCredentialFill(t, git, nil, nil)
	require.Error(t, err)
	require.Contains(t, stderr, "terminal prompts disabled")
}

func TestGitAskpassBrokerCancellationAndIsolation(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	var calls atomic.Int32
	helper, address, token, cleanup, err := startGitAuthBroker(context.Background(), exe, "git fetch", func(context.Context, GitAuthRequest) (string, error) {
		calls.Add(1)
		return "", errors.New("user canceled input with private details")
	})
	require.NoError(t, err)
	defer cleanup()
	request := func(token string) (gitAskpassResponse, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		conn, err := dialGitAuth(ctx, address)
		if err != nil {
			return gitAskpassResponse{}, err
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(time.Second))
		err = json.NewEncoder(conn).Encode(gitAskpassRequest{Token: token, Prompt: "Password for 'https://example.invalid': "})
		if err != nil {
			return gitAskpassResponse{}, err
		}
		var response gitAskpassResponse
		err = json.NewDecoder(conn).Decode(&response)
		return response, err
	}
	_, err = request(strings.Repeat("0", 64))
	require.Error(t, err)
	require.Zero(t, calls.Load())
	response, err := request(token)
	require.NoError(t, err)
	require.Contains(t, response.Error, "canceled")
	require.NotContains(t, response.Error, "private details")
	response, err = request(token)
	require.NoError(t, err)
	require.Contains(t, response.Error, "canceled")
	require.EqualValues(t, 1, calls.Load())
	cleanup()
	cleanup()
	_, err = os.Stat(filepath.Dir(helper))
	require.True(t, os.IsNotExist(err))
}

func TestGitAuthTimeoutClosesPendingRequest(t *testing.T) {
	git := isolateGitAuth(t)
	entered := make(chan struct{})
	var helper string
	started := time.Now()
	result, err := (commandRunner{
		Timeout: time.Second,
		BuildCommand: func(ctx context.Context) *exec.Cmd {
			cmd := exec.CommandContext(ctx, git, "credential", "fill")
			cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
			return cmd
		},
		Prepare: func(ctx context.Context, cmd *exec.Cmd) (func(), error) {
			cleanup, err := prepareGitAuth(ctx, cmd, "git fetch", func(ctx context.Context, _ GitAuthRequest) (string, error) {
				close(entered)
				<-ctx.Done()
				return "", ctx.Err()
			})
			helper = envValue(cmd.Env, "SSH_ASKPASS")
			return cleanup, err
		},
	}).Run()
	require.NoError(t, err)
	require.True(t, result.TimedOut)
	select {
	case <-entered:
	default:
		t.Fatal("Git did not reach authentication")
	}
	require.Less(t, time.Since(started), 4*time.Second)
	_, err = os.Stat(helper)
	require.True(t, os.IsNotExist(err))
}

func TestGitAuthEnvironmentAndLauncher(t *testing.T) {
	isolateGitAuth(t)
	cmd := exec.Command("unused")
	cmd.Env = commandEnv(os.Environ(), map[string]string{"SSH_ASKPASS": "existing", "GIT_TERMINAL_PROMPT": "1"})
	cleanup, err := prepareGitAuth(context.Background(), cmd, "unused", func(context.Context, GitAuthRequest) (string, error) { return "", nil })
	require.NoError(t, err)
	cleanup()
	require.Equal(t, "existing", envValue(cmd.Env, "SSH_ASKPASS"))
	require.Equal(t, "0", envValue(cmd.Env, "GIT_TERMINAL_PROMPT"))
	if runtime.GOOS == "windows" {
		env := commandEnv([]string{"git_terminal_prompt=1"}, map[string]string{"GIT_TERMINAL_PROMPT": "0"})
		require.Len(t, env, 1)
	}
	// A separate executable path proves both shell quoting and Git's literal
	// askpass path handling, including spaces, apostrophes and Unicode.
	exe, err := os.Executable()
	require.NoError(t, err)
	dest := filepath.Join(t.TempDir(), "mods 用户's helper.exe")
	data, err := os.ReadFile(exe)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(dest, data, 0o700))
	helper, address, token, cleanup, err := startGitAuthBroker(context.Background(), dest, "git fetch", func(context.Context, GitAuthRequest) (string, error) { return "literal-answer", nil })
	require.NoError(t, err)
	defer cleanup()
	git, err := exec.LookPath("git")
	require.NoError(t, err)
	cmd = exec.Command(git, "credential", "fill")
	cmd.Env = commandEnv(os.Environ(), map[string]string{"SSH_ASKPASS": helper, gitAuthAddressEnv: address, gitAuthTokenEnv: token, "GIT_TERMINAL_PROMPT": "0"})
	cmd.Stdin = strings.NewReader("protocol=https\nhost=example.invalid\n\n")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	require.Contains(t, string(out), "password=literal-answer")
}

func TestGitAskpassRejectsUnsupportedAndInvalidInput(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	for _, tc := range []struct {
		name, prompt, value, want string
		calls                     int32
	}{
		{"ssh", "Enter passphrase for key '/tmp/key':", "", "HTTP(S)", 0},
		{"newline", "Password for 'https://example.invalid':", "one\ntwo", "single-line", 1},
		{"oversized value", "Password for 'https://example.invalid':", strings.Repeat("s", 4097), "single-line", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			_, address, token, cleanup, err := startGitAuthBroker(context.Background(), exe, "git", func(context.Context, GitAuthRequest) (string, error) { calls.Add(1); return tc.value, nil })
			require.NoError(t, err)
			defer cleanup()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			conn, err := dialGitAuth(ctx, address)
			require.NoError(t, err)
			defer conn.Close()
			_ = conn.SetDeadline(time.Now().Add(time.Second))
			require.NoError(t, json.NewEncoder(conn).Encode(gitAskpassRequest{Token: token, Prompt: tc.prompt}))
			var response gitAskpassResponse
			require.NoError(t, json.NewDecoder(conn).Decode(&response))
			require.Contains(t, response.Error, tc.want)
			require.Empty(t, response.Value)
			require.Equal(t, tc.calls, calls.Load())
		})
	}
}

func TestGitAskpassCleanupClosesIdleConnections(t *testing.T) {
	exe, err := os.Executable()
	require.NoError(t, err)
	_, address, _, cleanup, err := startGitAuthBroker(context.Background(), exe, "git", nil)
	require.NoError(t, err)
	defer cleanup()
	conn, err := dialGitAuth(context.Background(), address)
	require.NoError(t, err)
	defer conn.Close()
	// Leave a partial JSON request in flight: cleanup must interrupt decoding.
	_, err = conn.Write([]byte(`{"token":`))
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { cleanup(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cleanup blocked on idle client")
	}
}
