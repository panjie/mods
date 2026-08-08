//go:build !windows

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrepareSudoCommandNonInteractive(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo rm /tmp/example", nil)
	defer cleanup()
	require.NoError(t, err)
	require.Contains(t, prepared.Command, "sudo -n rm")
	require.Empty(t, prepared.Env)
}

func TestPrepareSudoCommandInteractive(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo -u root rm /tmp/example", func(context.Context, string, string) (string, error) {
		return "password", nil
	})
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skip("sandbox does not permit Unix-domain listeners")
	}
	require.NoError(t, err)
	defer cleanup()
	require.Contains(t, prepared.Command, "sudo -A -u root rm")
	require.NotEmpty(t, prepared.Env["SUDO_ASKPASS"])
}

func TestPrepareSudoCommandModes(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo -n true", nil)
	defer cleanup()
	require.NoError(t, err)
	require.Equal(t, "sudo -n true", prepared.Command)

	_, cleanup, err = prepareSudoCommand(context.Background(), "printf x | sudo -S cat", nil)
	defer cleanup()
	require.ErrorContains(t, err, "sudo -S")

	_, cleanup, err = prepareSudoCommand(context.Background(), `sh -c "sudo true"`, nil)
	defer cleanup()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "nested") || strings.Contains(err.Error(), "parsed safely"))
}

func TestPrepareSudoCommandIgnoresCommandArgumentsThatLookLikeAuthFlags(t *testing.T) {
	prepared, cleanup, err := prepareSudoCommand(context.Background(), "sudo printf %s -n", func(context.Context, string, string) (string, error) {
		return "password", nil
	})
	if err != nil && strings.Contains(err.Error(), "operation not permitted") {
		t.Skip("sandbox does not permit Unix-domain listeners")
	}
	require.NoError(t, err)
	defer cleanup()
	require.Contains(t, prepared.Command, "sudo -A printf %s -n")
	require.NotEmpty(t, prepared.Env["SUDO_ASKPASS"])
}

func TestPrepareSudoProcess(t *testing.T) {
	prompt := func(context.Context, string, string) (string, error) { return "password", nil }

	t.Run("interactive", func(t *testing.T) {
		prepared, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/sudo", []string{"-u", "root", "launchctl", "disable", "gui/501/example"}, prompt, "sudo launchctl disable gui/501/example")
		if err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("sandbox does not permit Unix-domain listeners")
		}
		require.NoError(t, err)
		defer cleanup()
		require.Equal(t, []string{"-A", "-u", "root", "launchctl", "disable", "gui/501/example"}, prepared.Args)
		require.NotEmpty(t, prepared.Env["SUDO_ASKPASS"])
	})

	t.Run("non-interactive", func(t *testing.T) {
		prepared, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/sudo", []string{"true"}, nil, "sudo true")
		defer cleanup()
		require.NoError(t, err)
		require.Equal(t, []string{"-n", "true"}, prepared.Args)
		require.Empty(t, prepared.Env)
	})

	t.Run("explicit non-interactive", func(t *testing.T) {
		prepared, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/sudo", []string{"-u", "root", "-n", "true"}, prompt, "sudo -u root -n true")
		defer cleanup()
		require.NoError(t, err)
		require.Equal(t, []string{"-u", "root", "-n", "true"}, prepared.Args)
		require.Empty(t, prepared.Env)
	})

	t.Run("stdin rejected", func(t *testing.T) {
		_, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/sudo", []string{"-u", "root", "-S", "true"}, prompt, "sudo -u root -S true")
		defer cleanup()
		require.ErrorContains(t, err, "sudo -S")
	})

	t.Run("argument after command does not select mode", func(t *testing.T) {
		prepared, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/sudo", []string{"printf", "%s", "-n"}, prompt, "sudo printf %s -n")
		if err != nil && strings.Contains(err.Error(), "operation not permitted") {
			t.Skip("sandbox does not permit Unix-domain listeners")
		}
		require.NoError(t, err)
		defer cleanup()
		require.Equal(t, []string{"-A", "printf", "%s", "-n"}, prepared.Args)
	})

	t.Run("other executable unchanged", func(t *testing.T) {
		args := []string{"sudo", "true"}
		prepared, cleanup, err := prepareSudoProcess(context.Background(), "/usr/bin/env", args, prompt, "env sudo true")
		defer cleanup()
		require.NoError(t, err)
		require.Equal(t, args, prepared.Args)
		require.Empty(t, prepared.Env)
	})
}

func TestProcessRunDirectSudoUsesAskpass(t *testing.T) {
	root := t.TempDir()
	fakeSudo := filepath.Join(root, "sudo")
	require.NoError(t, os.WriteFile(fakeSudo, []byte("#!/bin/sh\nprintf 'args=%s\\n' \"$*\"\nprintf 'askpass=%s\\n' \"${SUDO_ASKPASS:-}\"\nprintf 'existing=%s\\n' \"${EXISTING_SECRET:-}\"\n"), 0o700))

	cfg := ProcessConfig{
		Root:    root,
		Timeout: 2 * time.Second,
		SudoPrompt: func(context.Context, string, string) (string, error) {
			return "password", nil
		},
	}
	out, err := runProcess(context.Background(), cfg, root, processRunArgs{
		Program:   fakeSudo,
		Args:      []string{"launchctl", "disable", "gui/501/example"},
		SecretEnv: map[string]string{"EXISTING_SECRET": "preserved"},
	})
	require.NoError(t, err)
	var result processRunResult
	require.NoError(t, json.Unmarshal([]byte(out), &result))
	require.NotNil(t, result.ExitCode)
	require.Zero(t, *result.ExitCode)
	require.Contains(t, result.Stdout, "args=-A launchctl disable gui/501/example")
	require.Regexp(t, `(?m)^askpass=.+/askpass\.sh$`, result.Stdout)
	require.Contains(t, result.Stdout, "existing=preserved")
}
