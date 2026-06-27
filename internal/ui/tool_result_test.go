package ui

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellResultBlock(t *testing.T) {
	shellArgs := []byte(`{"command":"ls -la"}`)

	t.Run("success", func(t *testing.T) {
		got := ShellResultBlock("shell_run", shellArgs, nil)
		require.Equal(t, "> \u2713 ran `ls -la` \u00b7 exit 0", got)
	})

	t.Run("exit error", func(t *testing.T) {
		err := exitCodeErr{code: 2}
		got := ShellResultBlock("shell_run", shellArgs, err)
		require.Equal(t, "> \u2717 ran `ls -la` \u00b7 exit 2", got)
	})

	t.Run("wrapped exit error", func(t *testing.T) {
		err := fmt.Errorf("boom: %w", exitCodeErr{code: 7})
		got := ShellResultBlock("shell_run", shellArgs, err)
		require.Equal(t, "> \u2717 ran `ls -la` \u00b7 exit 7", got)
	})

	t.Run("non exit error", func(t *testing.T) {
		err := errors.New("command not found")
		got := ShellResultBlock("shell_run", shellArgs, err)
		require.Equal(t, "> \u2717 ran `ls -la` \u00b7 failed: command not found", got)
	})

	t.Run("powershell", func(t *testing.T) {
		got := ShellResultBlock("powershell_run", []byte(`{"command":"Get-ChildItem"}`), nil)
		require.Equal(t, "> \u2713 ran `Get-ChildItem` \u00b7 exit 0", got)
	})

	t.Run("non shell tool is empty", func(t *testing.T) {
		require.Empty(t, ShellResultBlock("fs_read_file", []byte(`{"path":"/a"}`), nil))
	})

	t.Run("missing command is empty", func(t *testing.T) {
		require.Empty(t, ShellResultBlock("shell_run", []byte(`{}`), nil))
	})

	t.Run("command collapsed to one line", func(t *testing.T) {
		got := ShellResultBlock("shell_run", []byte(`{"command":"echo a\n echo b"}`), nil)
		require.Equal(t, "> \u2713 ran `echo a` \u00b7 exit 0", got)
	})

	t.Run("leading comment hidden in preview", func(t *testing.T) {
		got := ShellResultBlock("shell_run", []byte(`{"command":"# probe workspace config\nls .opencode*"}`), exitCodeErr{code: 1})
		require.Equal(t, "> \u2717 ran `ls .opencode*` \u00b7 exit 1", got)
	})
}

func TestShellResultBlockBackticks(t *testing.T) {
	// Command containing a backtick run must be enclosed by a longer fence,
	// padded with spaces because it ends with a backtick.
	got := ShellResultBlock("shell_run", []byte(`{"command":"echo `+"`"+`date`+"`"+`"}`), nil)
	require.Contains(t, got, "ran `` echo")
	require.Contains(t, got, "`date` ``")
}

type exitCodeErr struct{ code int }

func (e exitCodeErr) Error() string { return fmt.Sprintf("exit %d", e.code) }
func (e exitCodeErr) ExitCode() int { return e.code }
