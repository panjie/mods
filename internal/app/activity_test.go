package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/secrets"
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
	require.Contains(t, ansi.Strip(m.glamOutput), "diagnostic output", "failed results expand without a keypress")
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

func TestActivityDistinguishesReorganizationFromExecutionFailure(t *testing.T) {
	withOutputTTY(t, true)
	old := IsErrorTTY
	IsErrorTTY = func() bool { return true }
	t.Cleanup(func() { IsErrorTTY = old })
	m := newTodoTestMods(t)
	m.Config.InteractiveTTYAvailable = true
	m.appendToOutput("Answer\n\n")
	m.toolResultOutputCmd("shell_run", []byte(`{"command":"unknown"}`), &commandReorganizationError{ReasonCodes: []string{"user_requested"}}, "control")
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)
	require.Len(t, m.activityBlocks[marker], 1)
	record := m.activityBlocks[marker][0]
	require.False(t, record.failed)
	require.Contains(t, record.status, "reorganization requested")
	require.NotContains(t, record.status, "failed")
	require.NotContains(t, record.status, "exit")
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
