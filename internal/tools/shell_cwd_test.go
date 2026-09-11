package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestShellLiteralCwd covers the literal read-only execution context of the
// shell tool: an existing cwd must be honored verbatim (including paths with
// spaces, non-ASCII characters, and shell metacharacters), and a missing cwd
// must fail rather than silently falling back to the workspace.
//
// It intentionally lives in a file without build tags: command_review_windows_test.go
// forwards the Windows reliability lanes to it (TestWindowsReliabilityLiteralShellCwd),
// and the ordinary cross-platform suite runs it directly.
//
// Restored after 2f51fcf ("remove script_run") deleted script_test.go and took
// this unrelated test with it, leaving the forwarding reference in
// command_review_windows_test.go pointing at an undefined symbol and breaking
// the whole internal/tools test binary on Windows.
func TestShellLiteralCwd(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "中文 path $literal")
	require.NoError(t, os.Mkdir(target, 0700))
	registry := NewRegistry()
	require.NoError(t, RegisterShell(registry, ShellConfig{Root: root}))
	command := "pwd"
	if runtime.GOOS == "windows" {
		command = "(Get-Location).Path"
	}
	data, _ := json.Marshal(map[string]string{"command": command, "cwd": target})
	out, err := registry.Call(context.Background(), "shell_run", data)
	require.NoError(t, err)
	require.Contains(t, out, filepath.Base(target))
	data, _ = json.Marshal(map[string]string{"command": command, "cwd": filepath.Join(root, "missing")})
	_, err = registry.Call(context.Background(), "shell_run", data)
	require.Error(t, err)
}
