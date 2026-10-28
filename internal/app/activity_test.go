package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/secrets"
	"github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestActivityCollapseExpandAndRetryPreserveRawOutput(t *testing.T) {
	withOutputTTY(t, true)
	old := IsErrorTTY
	IsErrorTTY = func() bool { return true }
	t.Cleanup(func() { IsErrorTTY = old })
	m := newTodoTestMods(t)
	m.Config.InteractiveTTYAvailable = true
	m.width, m.height = 80, 24
	m.secrets = secrets.New()
	m.secrets.RememberForRedaction("secret-token")
	m.appendToOutput("Answer\n\n")
	raw := m.Output
	m.toolResultOutputCmd("fs_read_file", []byte(`{"path":"a.go"}`), nil, "first file")
	m.toolResultOutputCmd("fs_read_file", []byte(`{"path":"b.go"}`), nil, "second secret-token\x1b[2J\a")
	collapsed := ansi.Strip(m.glamOutput)
	require.Contains(t, collapsed, "2 operations")
	require.NotContains(t, collapsed, "first file")
	require.Equal(t, raw, m.Output)
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	expanded := ansi.Strip(m.glamOutput)
	require.Contains(t, expanded, "first file")
	require.Contains(t, expanded, "a.go")
	require.Contains(t, expanded, "b.go")
	require.NotContains(t, expanded, "secret-token")
	require.NotContains(t, expanded, "\a")
	require.Equal(t, raw, m.Output)
	m.toolResultOutputCmd("shell_run", []byte(`{"command":"go test ./..."}`), errors.New("test failed"), "diagnostic output")
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.NotContains(t, ansi.Strip(m.glamOutput), "diagnostic output", "failed results stay collapsed")
	require.NotContains(t, ansi.Strip(m.glamOutput), "Arguments:")
	require.Contains(t, ansi.Strip(m.glamOutput), "✗ shell")
	require.Contains(t, ansi.Strip(m.glamOutput), "test failed", "collapsed failures retain a diagnostic")
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.Contains(t, ansi.Strip(m.glamOutput), "diagnostic output")
	require.Contains(t, ansi.Strip(m.glamOutput), "test failed")
	m.saveCompletionCheckpoint(nil)
	marker, _ := m.trailingToolResultBlockMarker()
	m.activityBlocks[marker][0].detail = "changed"
	m.outputBuilder.Reset()
	m.displayOutputBuilder.Reset()
	m.restoreCompletionOutput()
	require.Equal(t, "Tool: fs_read_file\nArguments: {\"path\":\"a.go\"}\nfirst file", m.activityBlocks[marker][0].detail)
	m.width = 30
	m.renderDirty = true
	m.flushRender()
	for _, line := range strings.Split(m.renderActivity(m.activityBlocks[marker]), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 30)
	}
}

func TestActivityHiddenModesDoNotLeakHints(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 80, 24
	m.activityBlocks = map[string][]activityRecord{"x": {{status: "read"}}}
	m.Config.HideToolStatus = true
	require.Empty(t, m.activityHelp())
	m.Config.HideToolStatus = false
	m.Config.Raw = true
	require.Empty(t, m.activityHelp())
}

func TestActivityRedactsBeforeTruncationAndPreservesArguments(t *testing.T) {
	m := newTodoTestMods(t)
	m.secrets = secrets.New()
	m.secrets.RememberForRedaction("known-secret-with-a-long-suffix")
	data := []byte(`{"command":"echo known-secret-with-a-long-suffix https://alice:password@example.com/path?sig=private&token=other#fragment"}`)
	original := string(data)
	for _, width := range []int{25, 80, 400} {
		status := m.activityStatus("shell_run", data, nil, width)
		for _, secret := range []string{"known-sec", "alice", "password", "private", "other", "fragment"} {
			require.NotContains(t, status, secret)
		}
	}
	require.Equal(t, original, string(data))
	escaped := []byte(`{"command":"echo known\u002dsecret-with-a-long-suffix"}`)
	require.NotContains(t, string(m.activityDisplayArgs(escaped)), "known")
	m.appendToOutput("Answer\n\n")
	m.appendToolResultDisplayBlock("done")
	m.recordActivity("shell_run", "done", data, "https://example.com/?token=private&sig=other", errors.New("failed"))
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)
	detail := m.activityBlocks[marker][0].detail
	for _, secret := range []string{"known-secret", "password", "private", "other", "fragment"} {
		require.NotContains(t, detail, secret)
	}
	require.Contains(t, detail, "[redacted]")
}

func TestCollapsedActivityKeepsExitDiagnostic(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 80
	m.appendToolResultDisplayBlock("failed")
	data := []byte(`{"command":"go test ./..."}`)
	err := tools.ShellExitError{Code: 1}
	status := m.activityStatus("shell_run", data, err, 76)
	require.Contains(t, status, "exit 1")
	m.recordActivity("shell_run", status, data, "compile error: missing symbol\nmore details", err)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)
	view := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, view, "missing symbol")
	require.NotContains(t, view, "Arguments:")
	require.NotContains(t, view, "more details")
}
