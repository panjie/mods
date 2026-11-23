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
	require.NotContains(t, expanded, "first file", "expanding reveals statuses, not output")
	require.NotContains(t, expanded, "Tool:")
	require.NotContains(t, expanded, "Arguments:")
	require.NotContains(t, expanded, "2 operations")
	require.Contains(t, expanded, "a.go")
	require.Contains(t, expanded, "b.go")
	require.NotContains(t, expanded, "secret-token")
	require.NotContains(t, expanded, "\a")
	require.Equal(t, raw, m.Output)
	m.toolResultOutputCmd("shell_run", []byte(`{"command":"go test ./..."}`), errors.New("test failed"), "diagnostic output")
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.NotContains(t, ansi.Strip(m.glamOutput), "diagnostic output")
	require.NotContains(t, ansi.Strip(m.glamOutput), "Arguments:")
	require.Contains(t, ansi.Strip(m.glamOutput), "✗ shell")
	require.Contains(t, ansi.Strip(m.glamOutput), "test failed", "collapsed failures retain a diagnostic")
	m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
	require.NotContains(t, ansi.Strip(m.glamOutput), "diagnostic output", "expanding never opens result details")
	require.Contains(t, ansi.Strip(m.glamOutput), "test failed")
	m.saveCompletionCheckpoint(nil)
	marker, _ := m.trailingToolResultBlockMarker()
	savedStatus := m.activityBlocks[marker][0].status
	m.activityBlocks[marker][0].status = "changed"
	m.outputBuilder.Reset()
	m.displayOutputBuilder.Reset()
	m.restoreCompletionOutput()
	require.Equal(t, savedStatus, m.activityBlocks[marker][0].status)
	require.Equal(t, raw, m.Output)
	m.width = 30
	m.renderDirty = true
	m.flushRender()
	for _, line := range strings.Split(m.renderActivity(m.activityBlocks[marker]), "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 30)
	}
}

func TestCollapsedActivityFoldsSuccessfulCommandRunsToOneLine(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityStatusLimit)
		m.recordActivity(name, status, output, err)
	}
	record("shell_run", []byte(`{"command":"docker ps -a"}`), "container ids", nil)
	record("shell_run", []byte(`{"command":"ls -l /dev/kvm"}`), "crw-rw----", nil)
	record("shell_run", []byte(`{"command":"vgcfgrestore -l"}`), "snapshots", nil)
	record("web_search", []byte(`{"query":"qemu docker"}`), "results", nil)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	lines := strings.Split(collapsed, "\n")
	require.Len(t, lines, 1, "a successful run folds to one rail line")
	require.Contains(t, lines[0], "×4", "the run size heads the folded line")
	require.Contains(t, lines[0], "search web", "the newest status stays visible")
	require.NotContains(t, collapsed, "docker ps")
	require.NotContains(t, collapsed, "container ids")

	// The count must survive narrow terminals: it sits ahead of the status
	// text that truncation trims.
	m.width = 40
	narrow := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Len(t, strings.Split(narrow, "\n"), 1)
	require.Contains(t, strings.Split(narrow, "\n")[0], "×4")
	for _, line := range strings.Split(narrow, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 40)
	}
	m.width = 100

	m.activityExpanded = true
	expanded := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Len(t, strings.Split(expanded, "\n"), 4, "each operation expands to one status line")
	for _, want := range []string{"docker ps -a", "ls -l /dev/kvm", "vgcfgrestore -l", "search web"} {
		require.Contains(t, expanded, want)
	}
	for _, detail := range []string{"container ids", "crw-rw----", "snapshots", "Tool:", "Arguments:"} {
		require.NotContains(t, expanded, detail)
	}
	require.NotContains(t, expanded, "×4")
}

func TestCollapsedActivityFoldsRunsAcrossInterleavedReads(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityStatusLimit)
		m.recordActivity(name, status, output, err)
	}
	record("shell_run", []byte(`{"command":"grep -rniE \"voice|speech\" ~/.config/hypr/"}`), "matches", nil)
	record("fs_read_file", []byte(`{"path":"/home/panjie/.config/hypr/bindings.lua"}`), "bindings", nil)
	record("shell_run", []byte(`{"command":"grep -rn voxtype ~/.config/hypr/"}`), "voxtype bindings", nil)
	record("shell_run", []byte(`{"command":"grep -rn voxtype ~/.config/voxtype/"}`), "voxtype config", nil)
	record("shell_run", []byte(`{"command":"sed -n 1,30p /usr/share/omarchy/default/hypr/bindings/dictation.lua"}`), "", tools.ShellExitError{Code: 1})
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	lines := strings.Split(collapsed, "\n")
	require.Len(t, lines, 3, "folded run + failed status + diagnostic")
	require.Contains(t, lines[0], "×4", "the read folds into the counted run instead of splitting it")
	require.Contains(t, lines[0], "grep -rn voxtype ~/.config/voxtype/", "the newest status stays visible")
	require.NotContains(t, collapsed, "bindings.lua", "the interleaved read is folded away")
	require.Contains(t, lines[1], "✗ shell")
	require.Contains(t, lines[2], "exited with status 1")
}

func TestCollapsedActivityPinsSkillLoads(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityStatusLimit)
		m.recordActivity(name, status, output, err)
	}
	record("shell_run", []byte(`{"command":"ls -la"}`), "files", nil)
	record("load_skill", []byte(`{"name":"omarchy"}`), "skill body", nil)
	record("shell_run", []byte(`{"command":"grep -rn voxtype ~/.config/hypr/"}`), "bindings", nil)
	record("shell_run", []byte(`{"command":"docker ps -a"}`), "containers", nil)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	lines := strings.Split(collapsed, "\n")
	require.Len(t, lines, 3, "skill load splits the run into two folds plus its own line")
	require.Contains(t, lines[0], "ls -la", "the lone command before the skill keeps its own line")
	require.Contains(t, lines[1], "skill  omarchy", "skill loads never fold and stay visible")
	require.Contains(t, lines[2], "×2", "commands after the skill fold separately")
	require.Contains(t, lines[2], "docker ps -a")
	require.NotContains(t, collapsed, "skill body")
}

func TestCollapsedActivityKeepsFailuresVisibleOutsideFoldedRuns(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityStatusLimit)
		m.recordActivity(name, status, output, err)
	}
	record("shell_run", []byte(`{"command":"docker ps -a"}`), "container ids", nil)
	record("shell_run", []byte(`{"command":"go test ./..."}`), "compile error: missing symbol", tools.ShellExitError{Code: 1})
	record("shell_run", []byte(`{"command":"git diff"}`), "diff", nil)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, collapsed, "docker ps -a", "records before the failure stay visible")
	require.Contains(t, collapsed, "✗ shell")
	require.Contains(t, collapsed, "missing symbol", "collapsed failures retain a diagnostic")
	require.Contains(t, collapsed, "git diff", "records after the failure stay visible")
}

func TestActivityHiddenModesDoNotLeakHints(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 80, 24
	m.activityBlocks = map[string][]activityRecord{"x": {{status: "read a"}, {status: "read b"}}}
	require.Contains(t, m.activityHelp(), "Ctrl+O expand")
	m.Config.HideToolStatus = true
	require.Empty(t, m.activityHelp())
	m.Config.HideToolStatus = false
	m.Config.Raw = true
	require.Empty(t, m.activityHelp())
}

func TestActivityToggleRequiresFoldedGroup(t *testing.T) {
	withOutputTTY(t, true)
	for _, tc := range []struct {
		name   string
		blocks map[string][]activityRecord
		folds  bool
	}{
		{name: "no completed operations"},
		{name: "single command", blocks: map[string][]activityRecord{
			"a": {{status: "✓ shell  git status"}},
		}},
		{name: "failure splits commands", blocks: map[string][]activityRecord{
			"a": {{status: "✓ shell  git status"}, {failure: "failed"}, {status: "✓ shell  git diff"}},
		}},
		{name: "skill splits commands", blocks: map[string][]activityRecord{
			"a": {{status: "✓ shell  git status"}, {pinned: true}, {status: "✓ shell  git diff"}},
		}},
		{name: "commands in separate blocks", blocks: map[string][]activityRecord{
			"a": {{status: "✓ shell  git status"}},
			"b": {{status: "✓ shell  git diff"}},
		}},
		{name: "consecutive commands", blocks: map[string][]activityRecord{
			"a": {{status: "✓ shell  git status"}, {status: "✓ shell  git diff"}},
		}, folds: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTodoTestMods(t)
			m.width, m.height = 80, 24
			m.activityBlocks = tc.blocks
			if tc.folds {
				require.Contains(t, m.activityHelp(), "Ctrl+O expand")
			} else {
				require.Empty(t, m.activityHelp())
			}
			m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
			require.Equal(t, tc.folds, m.activityExpanded)
			if tc.folds {
				require.Contains(t, m.activityHelp(), "Ctrl+O collapse")
			} else {
				require.Empty(t, m.activityHelp())
			}
			m.Update(tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})
			require.False(t, m.activityExpanded)
		})
	}
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
	m.width = 400
	status := m.activityStatus("shell_run", data, nil, activityStatusLimit)
	m.recordActivity("shell_run", status, "known-secret-with-a-long-suffix https://example.com/?token=private&sig=other\a", tools.ShellExitError{Code: 1})
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)
	for _, expanded := range []bool{false, true} {
		m.activityExpanded = expanded
		view := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
		for _, secret := range []string{"known-secret", "password", "private", "other", "fragment", "\a"} {
			require.NotContains(t, view, secret)
		}
		require.Contains(t, view, "[redacted]")
	}
}

func TestCollapsedActivityKeepsExitDiagnostic(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 80
	m.appendToolResultDisplayBlock("failed")
	data := []byte(`{"command":"go test ./..."}`)
	err := tools.ShellExitError{Code: 1}
	status := m.activityStatus("shell_run", data, err, 76)
	require.Contains(t, status, "exit 1")
	m.recordActivity("shell_run", status, "compile error: missing symbol\nmore details", err)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)
	view := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, view, "missing symbol")
	require.NotContains(t, view, "Arguments:")
	require.NotContains(t, view, "more details")
}
