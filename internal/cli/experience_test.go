package cli

import (
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestHelpQuickStartPrecedesAdvancedAndWraps(t *testing.T) {
	out := captureStdout(t, func() { require.NoError(t, usageFunc(rootCmd)) })
	require.Less(t, strings.Index(out, "Quick start:"), strings.Index(out, "Options:"))
	require.Less(t, strings.Index(out, "--chat"), strings.Index(out, "Advanced options:"))
	for _, width := range []int{24, 40, 80, 120} {
		for _, name := range []string{"model", "word-wrap", "max-retries"} {
			text := renderFlag(rootCmd.Flags().Lookup(name), width)
			require.Contains(t, ansi.Strip(text), "--"+name)
			for _, line := range strings.Split(text, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), width, line)
			}
		}
	}
}

func TestSetupProgressAndStableActionsDuringDiscovery(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.page = setupDiscovery
		m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
		m.busy = "Discovering models"
		before := ansi.Strip(m.View().Content)
		require.Contains(t, before, "Step ")
		require.Contains(t, before, "Discovering models…")
		footer := lineIndexForExperience(before, "cancel")
		m.busy = ""
		m.err = errors.New("No models returned")
		failed := ansi.Strip(m.View().Content)
		require.Contains(t, failed, "No models returned")
		require.Contains(t, failed, "manually")
		require.Equal(t, footer, lineIndexForExperience(failed, "cancel"))
		m.err = nil
		m.draft().discovered = true
		m.draft().models = []string{"model-a", "model-b"}
		succeeded := ansi.Strip(m.View().Content)
		require.Contains(t, succeeded, "Found 2 models")
		require.Equal(t, footer, lineIndexForExperience(succeeded, "cancel"))
	})
}

func lineIndexForExperience(s, needle string) int {
	for i, line := range strings.Split(s, "\n") {
		if strings.Contains(line, needle) {
			return i
		}
	}
	return -1
}

func TestErrorRecoveryGuidance(t *testing.T) {
	require.Contains(t, errorRecoveryHint(&os.PathError{Op: "open", Path: "config", Err: os.ErrPermission}), "permissions")
	require.Contains(t, errorRecoveryHint(&net.DNSError{Err: "lookup failed", Name: "provider.example"}), "mods --config")
	require.Empty(t, errorRecoveryHint(errSetupCanceled))
	require.Empty(t, errorRecoveryHint(errors.New("unclassified failure")))
}
