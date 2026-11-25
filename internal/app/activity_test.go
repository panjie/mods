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
	require.Contains(t, collapsed, "2 succeeded")
	require.Contains(t, collapsed, "0 failed")
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
	folded := ansi.Strip(m.glamOutput)
	require.NotContains(t, folded, "diagnostic output", "failed results stay collapsed")
	require.NotContains(t, folded, "Arguments:")
	require.Contains(t, folded, "✗")
	require.Contains(t, folded, "2 succeeded")
	require.Contains(t, folded, "1 failed", "collapsed failures still report the count")
	require.Contains(t, folded, "test failed", "the one-line status keeps the short failure reason")
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

func TestCollapsedActivitySummaryReportsSuccessAndFailureCounts(t *testing.T) {
	require.Equal(t,
		"✗ ×3  2 succeeded · 1 failed  ·  read  c.go",
		collapsedActivitySummary([]activityRecord{
			{status: "✓ shell  make  · exit 0"},
			{status: "✗ shell  make test  · exit 1", failed: true},
			{status: "✓ read  c.go", read: true},
		}),
	)
	require.Equal(t,
		"✓ Read files · 2 succeeded · 0 failed",
		collapsedActivitySummary([]activityRecord{
			{status: "✓ read  a.go", read: true},
			{status: "✓ read  b.go", read: true},
		}),
	)
}

func TestCollapsedActivityFoldsSuccessfulCommandRunsToOneLine(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityPreviewLimit)
		m.recordActivity(name, status, data, output, err)
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
	require.Contains(t, lines[0], "4 succeeded")
	require.Contains(t, lines[0], "0 failed")
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
	for _, want := range []string{"docker ps -a", "ls -l /dev/kvm", "vgcfgrestore -l", "container ids"} {
		require.Contains(t, expanded, want)
	}
	require.NotContains(t, expanded, "×4")
}

func TestCollapsedActivityFoldsRunsAcrossInterleavedReads(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityPreviewLimit)
		m.recordActivity(name, status, data, output, err)
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
	require.Len(t, lines, 1, "the mixed run folds into one summary line")
	require.Contains(t, lines[0], "✗ ×5", "the failure is reflected in the summary marker and total")
	require.Contains(t, lines[0], "4 succeeded")
	require.Contains(t, lines[0], "1 failed")
	require.Contains(t, lines[0], "sed -n 1,30p", "the newest status stays visible")
	require.NotContains(t, collapsed, "bindings.lua", "the interleaved read is folded away")
	require.NotContains(t, collapsed, "exited with status 1", "failure output waits for Ctrl+O")
}

func TestCollapsedActivityPinsSkillLoads(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityPreviewLimit)
		m.recordActivity(name, status, data, output, err)
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
	require.NotContains(t, collapsed, "skill body", "details still wait for Ctrl+O")
}

func TestCollapsedActivityFoldsFailuresIntoSummary(t *testing.T) {
	m := newTodoTestMods(t)
	m.width = 100
	m.appendToolResultDisplayBlock("start")
	record := func(name string, data []byte, output string, err error) {
		status := m.activityStatus(name, data, err, activityPreviewLimit)
		m.recordActivity(name, status, data, output, err)
	}
	record("shell_run", []byte(`{"command":"docker ps -a"}`), "container ids", nil)
	record("shell_run", []byte(`{"command":"go test ./..."}`), "compile error: missing symbol", tools.ShellExitError{Code: 1})
	record("shell_run", []byte(`{"command":"git diff"}`), "diff", nil)
	marker, ok := m.trailingToolResultBlockMarker()
	require.True(t, ok)

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	lines := strings.Split(collapsed, "\n")
	require.Len(t, lines, 1, "failures no longer split the folded run")
	require.Contains(t, lines[0], "✗ ×3")
	require.Contains(t, lines[0], "2 succeeded")
	require.Contains(t, lines[0], "1 failed")
	require.Contains(t, lines[0], "git diff", "the newest status stays visible")
	require.NotContains(t, collapsed, "docker ps -a", "older calls stay folded away")
	require.NotContains(t, collapsed, "missing symbol", "failure output waits for Ctrl+O")

	m.activityExpanded = true
	expanded := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, expanded, "docker ps -a")
	require.Contains(t, expanded, "missing symbol")
	require.Contains(t, expanded, "git diff")
	require.NotContains(t, expanded, "×3")
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

func TestCollapsedActivityHidesExitDiagnosticUntilExpanded(t *testing.T) {
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

	collapsed := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, collapsed, "✗ shell")
	require.Contains(t, collapsed, "exit 1")
	require.NotContains(t, collapsed, "missing symbol")
	require.NotContains(t, collapsed, "Arguments:")
	require.NotContains(t, collapsed, "more details")

	m.activityExpanded = true
	expanded := ansi.Strip(m.renderActivity(m.activityBlocks[marker]))
	require.Contains(t, expanded, "missing symbol")
	require.Contains(t, expanded, "more details")
}
