package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestSessionReviewFitsAndRequiresEveryPageAfterResize(t *testing.T) {
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
		name: "shell_run", resp: make(chan reviewResponse, 1),
		candidateRules: []Rule{{Type: approval.DirAllow, Mode: AccessWrite, Paths: []string{"/tmp/project"}}},
		presentation: reviewPresentation{headline: "Modify local files", tone: interactionToneWarning, toneText: "Warning", rows: []interactionRow{
			{Label: "Command", Value: strings.Repeat("echo 中文; ", 200) + "FINAL_COMMAND"},
			{Label: "Target", Value: "/tmp/project"},
		}},
	}})
	for _, size := range [][2]int{{80, 24}, {40, 16}} {
		all := ""
		view := r.renderBanner(size[0], makeStyles(true).Interaction, size[1])
		require.False(t, r.canApprovePages())
		for {
			require.LessOrEqual(t, lipgloss.Height(view), size[1])
			for _, line := range strings.Split(view, "\n") {
				require.LessOrEqual(t, lipgloss.Width(line), size[0])
			}
			all += ansi.Strip(view)
			if r.reviewPage+1 == r.reviewPages {
				break
			}
			r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
			view = r.renderBanner(size[0], makeStyles(true).Interaction, size[1])
		}
		require.True(t, r.canApprovePages())
		require.Contains(t, strings.ReplaceAll(strings.Join(strings.Fields(all), ""), "│", ""), "FINAL_COMMAND")
		require.Contains(t, all, "This conversation only")
		require.Contains(t, all, "Allow for session")
	}
}
