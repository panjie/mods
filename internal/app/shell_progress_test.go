package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestShellProgressStatus(t *testing.T) {
	got := shellProgressStatus(
		"powershell_run",
		"# installing PowerShell\nwinget install --id Microsoft.PowerShell",
		"\x1b[32mDownloading 42%\x1b[0m\r",
		200,
	)
	require.Equal(t, "PS - winget install --id Microsoft.PowerShell - last: Downloading 42%", got)
}

func TestShellProgressStatusUsesLastNonEmptyOutputLine(t *testing.T) {
	got := shellProgressStatus("shell_run", "npm install", "first\n\nlatest\n", 200)
	require.Equal(t, "Shell - npm install - last: latest", got)
}

func TestShellProgressStatusKeepsCommandWhenNarrow(t *testing.T) {
	got := shellProgressStatus("powershell_run", "winget install --id 9PFXXSHC64H3 --exact --accept-source-agreements --accept-package-agreements", "", 32)
	require.Contains(t, got, "PS - winget install")
	require.NotContains(t, got, "42s")
}

func TestToolCompletionStatus(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		got := toolCompletionStatus("shell_run", []byte(`{"command":"go test ./..."}`), nil, 200)
		require.Equal(t, "✓ shell_run: go test ./... · exit 0", got)
	})

	t.Run("exit code", func(t *testing.T) {
		got := toolCompletionStatus("powershell_run", []byte(`{"command":"npm test"}`), toolregistry.ShellExitError{Code: 1}, 200)
		require.Equal(t, "✗ powershell_run: npm test · exit 1", got)
	})

	t.Run("plain error", func(t *testing.T) {
		got := toolCompletionStatus("shell_run", []byte(`{"command":"missing"}`), errors.New("command not found"), 200)
		require.Equal(t, "✗ shell_run: missing · failed: command not found", got)
	})

	t.Run("non shell", func(t *testing.T) {
		got := toolCompletionStatus("fs_read_file", []byte(`{"path":"mods.go"}`), nil, 200)
		require.Equal(t, "✓ fs_read_file: path=mods.go", got)
	})
}

func TestHandleShellProgressRedactsSecrets(t *testing.T) {
	store := secrets.New()
	_, err := store.Put("super-secret", secrets.Target{Tool: "shell_run", Path: "/command"})
	require.NoError(t, err)

	m := &Mods{secrets: store, width: 200}
	ch := make(chan toolOperationStatusMsg, 1)
	m.setToolOperationChannel(ch)
	m.handleShellProgress(context.Background(), toolregistry.ShellProgress{
		Tool:       "shell_run",
		Command:    "echo super-secret",
		Elapsed:    time.Second,
		LastOutput: "super-secret",
	})

	msg := <-ch
	require.NotContains(t, msg.content, "super-secret")
	require.Contains(t, msg.content, "[REDACTED]")
}
