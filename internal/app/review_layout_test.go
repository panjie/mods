package app

import (
	"context"
	"encoding/json"
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
		require.Contains(t, compactReviewText(all), "thisconversationonly")
		require.Contains(t, all, "Allow for session")
	}
}

func compactReviewText(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(ansi.Strip(s)), ""), "│", "")
}

func newPagedTestReviewer(command string) *toolReviewer {
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
		name: "shell_run", resp: make(chan reviewResponse, 1),
		candidateRules: []Rule{{Type: approval.DirAllow, Mode: AccessWrite, Paths: []string{"/project"}}},
		presentation: reviewPresentation{headline: "Modify local files", tone: interactionToneWarning, toneText: "Warning", rows: []interactionRow{
			{Label: "Target", Value: "/project"}, {Label: "Command", Value: command},
		}},
	}})
	return r
}

func TestReviewSessionReferenceOnlyOnSinglePage(t *testing.T) {
	styles := makeStyles(true).Interaction
	r := newPagedTestReviewer("touch file")
	view := r.renderBanner(120, styles, 24)
	require.Equal(t, 1, r.reviewPages)
	require.Equal(t, 1, strings.Count(view, "/project"))
	require.Contains(t, view, "above directories")

	r = newPagedTestReviewer(strings.Repeat("echo long-command; ", 100))
	r.renderBanner(60, styles, 16)
	for r.reviewPage+1 < r.reviewPages {
		r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		view = r.renderBanner(60, styles, 16)
		require.NotContains(t, view, "above directories")
	}
	require.Contains(t, compactReviewText(view), "Writedirs:/project(includingsubdirectories)")
}

func TestReviewNavigationSkipsUnavailableActions(t *testing.T) {
	r := newPagedTestReviewer(strings.Repeat("echo long-command; ", 100))
	styles := makeStyles(true).Interaction
	view := r.renderBanner(60, styles, 16)
	require.Contains(t, view, "Review all pages to enable approval.")
	require.Equal(t, reviewOptionDeny, r.reviewOptions()[r.selected].action)
	r.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, reviewOptionCancel, r.reviewOptions()[r.selected].action)
	r.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, reviewOptionDeny, r.reviewOptions()[r.selected].action)
	r.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Equal(t, reviewOptionCancel, r.reviewOptions()[r.selected].action)
	for r.reviewPage+1 < r.reviewPages {
		r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		view = r.renderBanner(60, styles, 16)
	}
	require.NotContains(t, view, "Review all pages")
	require.Equal(t, reviewOptionCancel, r.reviewOptions()[r.selected].action, "unlocking must not change the selected action")
	r.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, reviewOptionApprove, r.reviewOptions()[r.selected].action)
	r.renderBanner(30, styles, 16)
	require.Equal(t, reviewOptionDeny, r.reviewOptions()[r.selected].action, "resize must move focus off hidden approval")
}

func TestReviewShowsAllTargetsWithoutSessionGrant(t *testing.T) {
	assessment := approval.CommandAssessment{
		Effect:               approval.EffectWrite,
		KnownDirs:            []string{"/one", "/two", "/three", "/four"},
		DynamicTargets:       []string{"$ONE", "$TWO", "$THREE", "$FOUR"},
		ProviderWriteTargets: []string{"HKLM:\\one", "HKLM:\\two", "HKLM:\\three", "HKLM:\\four"},
		RemoteOrigins:        []string{"https://one.example", "https://two.example", "https://three.example", "https://four.example"},
	}
	args, err := json.Marshal(map[string]string{"command": "custom-writer"})
	require.NoError(t, err)
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
		name: "shell_run", resp: make(chan reviewResponse, 1),
		presentation: formatReviewPresentationWithIntent("shell_run", args, assessment, WorkingDirScope("/cwd"), assessment.AccessIntent()),
	}})
	var all string
	for {
		all += compactReviewText(r.renderBanner(40, makeStyles(true).Interaction, 16))
		if r.reviewPage+1 == r.reviewPages {
			break
		}
		r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	}
	for _, group := range [][]string{assessment.KnownDirs, assessment.DynamicTargets, assessment.ProviderWriteTargets, assessment.RemoteOrigins} {
		for _, target := range group {
			require.Contains(t, all, target)
		}
	}
	require.NotContains(t, all, "+1more")
}

func TestReviewCancelStopsTaskInsteadOfDenying(t *testing.T) {
	for _, enter := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := newPagedTestReviewer("touch file")
		r.renderBanner(120, makeStyles(true).Interaction, 24)
		resp := r.reviewItem.resp
		m := &Mods{Config: &Config{}, ctx: ctx, ctxCancel: cancel, reviewer: r, userInput: newUserInputManager(&Config{})}
		key := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		if enter {
			r.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
			require.Equal(t, reviewOptionCancel, r.reviewOptions()[r.selected].action)
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		_, cmd := m.Update(key)
		require.NotNil(t, cmd)
		r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
		require.Empty(t, resp, "cancellation must not release the tool as a denial or approval")
		_, quit := m.Update(cmd())
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.False(t, r.isPending())
		require.Equal(t, doneState, m.state)
		require.NotNil(t, quit)
		require.IsType(t, quitMsg{}, quit())
	}
}

func TestReviewDenyKeepsTaskRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newPagedTestReviewer("touch file")
	resp := r.reviewItem.resp
	m := &Mods{Config: &Config{}, ctx: ctx, ctxCancel: cancel, reviewer: r, userInput: newUserInputManager(&Config{})}
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.False(t, (<-resp).approved)
	require.NoError(t, ctx.Err())
}

func TestSessionGrantOnlyReusesIdenticalTargets(t *testing.T) {
	rules := []Rule{{Type: approval.DirAllow, Mode: AccessWrite, Paths: []string{"/tmp/project", "/usr/bin"}}}
	rows := []interactionRow{{Label: "Target", Value: "/tmp/project, /usr/bin"}}
	label := sessionRuleLabel(rules, rows)
	require.Equal(t, "Write above directories and subdirectories; this conversation only", label)
	rows[0].Value = "/tmp/project/reader.py"
	label = sessionRuleLabel(rules, rows)
	require.Contains(t, label, "/tmp/project, /usr/bin")
	require.Contains(t, label, "including subdirectories")
	require.NotContains(t, label, "above")
}
