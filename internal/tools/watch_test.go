package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/watch"
	"github.com/stretchr/testify/require"
)

// Re-exec the test binary for a portable, real process fixture.
func TestWatchProcessHelper(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "mods-watch-wait" {
			_, _ = os.Stdout.WriteString("ready\n")
			time.Sleep(time.Hour)
			os.Exit(0)
		}
		if arg == "mods-watch-helper" {
			_, _ = os.Stdout.WriteString("observed stdout\n")
			_, _ = os.Stderr.WriteString("observed stderr\n")
			os.Exit(7)
		}
	}
}
func TestWatchedProcessSurvivesToolReturnAndReportsExit(t *testing.T) {
	invocation, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := watch.New(invocation, "never matches", time.Millisecond)
	require.NoError(t, err)
	defer m.Close()
	call, cancelCall := context.WithCancel(watch.WithManager(invocation, m))
	exe, err := os.Executable()
	require.NoError(t, err)
	_, err = startWatchedProcess(WithAuthorizedDirs(call, []string{filepath.Dir(exe)}), ProcessConfig{AllowWatch: true, Timeout: time.Second}, t.TempDir(), processRunArgs{Program: exe, Args: []string{"-test.run=^TestWatchProcessHelper$", "--", "mods-watch-helper"}})
	require.NoError(t, err)
	cancelCall()
	e, err := m.Next(invocation)
	require.NoError(t, err)
	require.True(t, e.Finished)
	require.Contains(t, e.Output, "observed stdout", e.Result)
	require.Contains(t, e.Output, "observed stderr")
	var result processRunResult
	require.NoError(t, json.Unmarshal([]byte(e.Result), &result))
	require.NotNil(t, result.ExitCode)
	require.Equal(t, 7, *result.ExitCode)
}
func TestWatchedProcessRejectsUnsupportedCredentials(t *testing.T) {
	m, err := watch.New(context.Background(), "", time.Second)
	require.NoError(t, err)
	defer m.Close()
	ctx := watch.WithManager(context.Background(), m)
	_, err = startWatchedProcess(ctx, ProcessConfig{AllowWatch: true}, t.TempDir(), processRunArgs{Elevated: true})
	require.Error(t, err)
	_, err = startWatchedProcess(ctx, ProcessConfig{AllowWatch: true}, t.TempDir(), processRunArgs{SecretEnv: map[string]string{"TOKEN": "secret"}})
	require.Error(t, err)
	require.Equal(t, "idle", m.Status().State)
}

func TestWatchedProcessStopsWithInvocation(t *testing.T) {
	invocation, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m, err := watch.New(invocation, "ready", time.Millisecond)
	require.NoError(t, err)
	defer m.Close()
	exe, err := os.Executable()
	require.NoError(t, err)
	ctx := WithAuthorizedDirs(watch.WithManager(invocation, m), []string{filepath.Dir(exe)})
	_, err = startWatchedProcess(ctx, ProcessConfig{AllowWatch: true, Timeout: time.Minute}, t.TempDir(), processRunArgs{Program: exe, Args: []string{"-test.run=^TestWatchProcessHelper$", "--", "mods-watch-wait"}})
	require.NoError(t, err)
	e, err := m.Next(invocation)
	require.NoError(t, err)
	require.Contains(t, e.Output, "ready")
	require.False(t, e.Finished)
	start := time.Now()
	m.Close()
	require.Less(t, time.Since(start), 3*time.Second)
}
