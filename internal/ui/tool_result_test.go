package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestToolResultLine(t *testing.T) {
	shellArgs := []byte(`{"command":"ls -la"}`)

	t.Run("shell success", func(t *testing.T) {
		got := ToolResultLine("shell_run", shellArgs, nil)
		require.Equal(t, "> \u2713 shell_run: ls -la \u00b7 exit 0", got)
	})

	t.Run("shell exit error", func(t *testing.T) {
		got := ToolResultLine("shell_run", shellArgs, exitCodeErr{code: 2})
		require.Equal(t, "> \u2717 shell_run: ls -la \u00b7 exit 2", got)
	})

	t.Run("wrapped shell exit error", func(t *testing.T) {
		err := fmt.Errorf("boom: %w", exitCodeErr{code: 7})
		got := ToolResultLine("shell_run", shellArgs, err)
		require.Equal(t, "> \u2717 shell_run: ls -la \u00b7 exit 7", got)
	})

	t.Run("non exit error", func(t *testing.T) {
		err := errors.New("command not found")
		got := ToolResultLine("shell_run", shellArgs, err)
		require.Equal(t, "> \u2717 shell_run: ls -la \u00b7 failed: command not found", got)
	})

	t.Run("powershell", func(t *testing.T) {
		got := ToolResultLine("powershell_run", []byte(`{"command":"Get-ChildItem"}`), nil)
		require.Equal(t, "> \u2713 powershell_run: Get-ChildItem \u00b7 exit 0", got)
	})

	t.Run("non shell tool", func(t *testing.T) {
		got := ToolResultLine("fs_read_file", []byte(`{"path":"/a"}`), nil)
		require.Equal(t, "> \u2713 fs_read_file: path=/a", got)
	})

	t.Run("missing command still shows tool", func(t *testing.T) {
		got := ToolResultLine("shell_run", []byte(`{}`), nil)
		require.Equal(t, "> \u2713 shell_run \u00b7 exit 0", got)
	})

	t.Run("command collapsed to one line", func(t *testing.T) {
		got := ToolResultLine("shell_run", []byte(`{"command":"echo a\n echo b"}`), nil)
		require.Equal(t, "> \u2713 shell_run: echo a \u00b7 exit 0", got)
	})

	t.Run("leading comment hidden in preview", func(t *testing.T) {
		got := ToolResultLine("shell_run", []byte(`{"command":"# probe workspace config\nls .opencode*"}`), exitCodeErr{code: 1})
		require.Equal(t, "> \u2717 shell_run: ls .opencode* \u00b7 exit 1", got)
	})
}

func TestToolResultStatusTruncates(t *testing.T) {
	got := ToolResultStatus("fs_search", []byte(`{"query":"very long query","path":"/repo"}`), nil, 24)
	require.Equal(t, "\u2713 fs_search: query=ve...", got)
}

func TestToolResultLineWidth(t *testing.T) {
	got := ToolResultLineWidth("fs_delete_file", []byte(`{"path":"C:\\Users\\panjie\\Downloads\\Designer3_transparent_fine_clean_4x.png"}`), errors.New("execution denied by review"), 48)
	require.Equal(t, "> \u2717 fs_delete_file: path=C:\\Users\\panj... \u00b7 failed", got)
	require.LessOrEqual(t, len([]rune(strings.TrimPrefix(got, "> "))), 48)
}

type exitCodeErr struct{ code int }

func (e exitCodeErr) Error() string { return fmt.Sprintf("exit %d", e.code) }
func (e exitCodeErr) ExitCode() int { return e.code }
