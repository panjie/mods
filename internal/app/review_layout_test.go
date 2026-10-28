package app

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/approval"

	"github.com/stretchr/testify/require"
)

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

func TestReviewResponseZeroAndUnknownActionsDeny(t *testing.T) {
	for _, action := range []reviewAction{reviewActionDeny, reviewAction(255)} {
		r := &toolReviewer{
			reviewMode: ReviewAuto, scope: WorkingDirScope("/cwd"), reviewAvailabilityKnown: true,
			interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1),
		}
		done := make(chan error, 1)
		go func() {
			done <- r.requestApproval(reviewerDeps{
				ctx:          context.Background(),
				accessIntent: AccessIntent{Class: AccessWrite, WriteCoverage: approval.CoverageIncomplete},
			}, "native_write", nil)
		}()
		item := receiveReviewItem(t, r.reviewChan)
		item.resp <- reviewResponse{action: action}
		require.ErrorContains(t, <-done, "execution denied by user")
	}
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
	require.Equal(t, reviewActionDeny, (<-resp).action)
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
