package app

import (
	"context"
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
		65*time.Second+400*time.Millisecond,
		"\x1b[32mDownloading 42%\x1b[0m\r",
		200,
	)
	require.Equal(t, "PS - 1m05s - last: Downloading 42% - winget install --id Microsoft.PowerShell", got)
}

func TestShellProgressStatusUsesLastNonEmptyOutputLine(t *testing.T) {
	got := shellProgressStatus("shell_run", "npm install", 3*time.Second, "first\n\nlatest\n", 200)
	require.Equal(t, "Shell - 3s - last: latest - npm install", got)
}

func TestShellProgressStatusKeepsElapsedWhenNarrow(t *testing.T) {
	got := shellProgressStatus("powershell_run", "winget install --id 9PFXXSHC64H3 --exact --accept-source-agreements --accept-package-agreements", 42*time.Second, "", 32)
	require.Contains(t, got, "PS - 42s")
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
