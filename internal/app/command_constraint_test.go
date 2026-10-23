package app

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestFullReviewRequiresEveryPage(t *testing.T) {
	rows := make([]interactionRow, 0, 40)
	for i := 0; i < 35; i++ {
		rows = append(rows, interactionRow{Label: "File", Value: fmt.Sprintf("dir/%d.txt", i)})
	}
	rows = append(rows, interactionRow{Label: "Source", Value: "https://example.com/x\x1b[2J"})
	p := reviewPresentation{tone: interactionToneDanger, toneText: "Danger", headline: "Download files", rows: rows}
	resp := make(chan reviewResponse, 1)
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{name: "http_download", presentation: p, resp: resp}})
	r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.Empty(t, resp)
	styles := makeStyles(true).Interaction
	all := r.renderBanner(60, styles, 22)
	require.False(t, r.canApprovePages())
	require.NotContains(t, all, "Allow once")
	r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.Empty(t, resp)
	for r.reviewPage+1 < r.reviewPages {
		r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		all += r.renderBanner(60, styles, 22)
	}
	require.True(t, r.canApprovePages())
	require.Contains(t, all, `\u001b[2J`)
	r.renderBanner(30, styles, 22)
	require.False(t, r.canApprovePages())
	r.renderBanner(60, styles, 22)
	for r.reviewPage+1 < r.reviewPages {
		r.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
		r.renderBanner(60, styles, 22)
	}
	r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	require.True(t, (<-resp).approved)
}

func TestFullReviewShowsPagesHintOnlyWhenContentPaginates(t *testing.T) {
	styles := makeStyles(true).Interaction
	newReviewer := func(rows []interactionRow) *toolReviewer {
		r := &toolReviewer{}
		r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
			name: "shell_run",
			presentation: reviewPresentation{
				tone: interactionToneWarning, toneText: "Warning",
				headline: "Modify local files", rows: rows,
			},
			resp: make(chan reviewResponse, 1),
		}})
		return r
	}
	single := newReviewer([]interactionRow{{Label: "Command", Value: "git stash list"}})
	singleBanner := single.renderBanner(60, styles, 22)
	require.Equal(t, 1, single.reviewPages)
	require.Contains(t, singleBanner, "Allow once")
	require.NotContains(t, singleBanner, "Pages")

	rows := make([]interactionRow, 0, 40)
	for i := 0; i < 35; i++ {
		rows = append(rows, interactionRow{Label: "File", Value: fmt.Sprintf("dir/%d.txt", i)})
	}
	multi := newReviewer(rows)
	multiBanner := multi.renderBanner(60, styles, 22)
	require.Greater(t, multi.reviewPages, 1)
	require.Contains(t, multiBanner, "Pages")
}

func TestUncertainEffectTargetPolicy(t *testing.T) {
	r := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope, raw: true}
	deps := reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: approval.AccessWrite, UncertainEffect: true}, safeDirs: []string{testApprovalScope.Value}}
	require.ErrorIs(t, r.requestApproval(deps, "shell_run", []byte(`{"command":"echo x"}`)), errReviewUnavailable,
		"an unknown effect without concrete targets still fails closed")

	temp := t.TempDir()
	deps.accessIntent = AccessIntent{WriteCoverage: testCompleteCoverage, Class: approval.AccessWrite, Dirs: []string{temp}, UncertainEffect: true}
	deps.safeDirs = []string{temp}
	require.NoError(t, r.requestApproval(deps, "shell_run", []byte(`{"command":"opaque /tmp"}`)),
		"an unknown effect whose only target is a safe temp dir needs no review")

	r.reviewMode = ReviewNever
	deps.accessIntent = AccessIntent{WriteCoverage: testCompleteCoverage, Class: approval.AccessWrite, UncertainEffect: true}
	require.NoError(t, r.requestApproval(deps, "shell_run", []byte(`{"command":"echo x"}`)))
}
