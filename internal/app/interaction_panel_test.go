package app

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestInteractionPanelFitsWidthsAndPreservesSecurityDetails(t *testing.T) {
	styles := makeStyles(true).Interaction
	command := "sudo rm /usr/local/bin/mods && printf 'the complete command remains visible'"
	for _, width := range []int{30, 60, 80, 120} {
		t.Run(name("width=", width), func(t *testing.T) {
			rendered := renderInteractionPanel(styles, width, interactionPanel{
				Title: "Review required", Meta: "shell_run", Tone: interactionToneDanger,
				ToneText: "Danger", Headline: "Delete a file outside the workspace",
				Rows: []interactionRow{{Label: "Command", Value: command}, {Label: "Scope", Value: "/usr/local/bin"}},
				Actions: []interactionAction{
					{Key: "Y", Label: "Allow once", Selected: true},
					{Key: "N", Label: "Deny"},
					{Key: "A", Label: "Always allow"},
					{Key: "Ctrl+C", Label: "Cancel"},
				},
			})
			for _, line := range strings.Split(rendered, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), width, line)
			}
			plain := ansi.Strip(rendered)
			normalized := strings.ReplaceAll(strings.Join(strings.Fields(plain), ""), "┃", "")
			require.Contains(t, normalized, strings.Join(strings.Fields(command), ""))
			assertTextOrder(t, plain, "Y", "Allow once", "N", "Deny", "A", "Always allow", "Ctrl+C", "Cancel")
		})
	}
}

func TestInteractionPanelNoColorKeepsSemanticLabels(t *testing.T) {
	styles := makeStyles(true).Interaction
	rendered := renderInteractionPanel(styles, 60, interactionPanel{
		Title: "Review required", Tone: interactionToneDanger, ToneText: "Danger",
		Headline: "Delete a file", Rows: []interactionRow{{Label: "Target", Value: "/tmp/file"}},
		Actions: []interactionAction{{Key: "Y", Label: "Allow"}, {Key: "N", Label: "Deny"}},
	})
	require.Contains(t, rendered, "┃")
	require.Contains(t, rendered, "REVIEW REQUIRED")
	require.Contains(t, rendered, "DANGER")
	require.Contains(t, rendered, "Target")
	require.Contains(t, rendered, "Y")
	require.Contains(t, rendered, "Allow")
}

func assertTextOrder(t *testing.T, text string, values ...string) {
	t.Helper()
	position := -1
	for _, value := range values {
		next := strings.Index(text[position+1:], value)
		require.NotEqualf(t, -1, next, "missing %q in %q", value, text)
		position += next + 1
	}
}
