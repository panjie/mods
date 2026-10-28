package app

import (
	"context"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestCommandFullControlDispatchAndLifetime(t *testing.T) {
	for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways} {
		for _, key := range []string{"f", "F", "enter"} {
			t.Run(string(mode)+"/"+key, func(t *testing.T) {
				m, caller, called, args := declaredCommandFixture(t, "process_run", mode, true)
				ctx := WithReviewSession(context.Background())
				m.reviewer.sharedFullControl = newSessionToolReviewer(ctx, m.Config).sharedFullControl
				args["effect"] = "may_write"
				request := proto.ToolCallRequest{Name: "process_run", Arguments: []byte(stringMustJSON(t, args))}
				done := make(chan error, 1)
				go func() { _, err := caller(request); done <- err }()
				item := receiveReviewItem(t, m.reviewer.reviewChan)
				m.reviewer.handleStartMsg(toolReviewStartMsg{item: item})
				msg := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
				if key == "enter" {
					for m.reviewer.reviewOptions()[m.reviewer.selected].action != reviewOptionFullControl {
						m.reviewer.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
					}
					msg = tea.KeyPressMsg{Code: tea.KeyEnter}
				}
				m.reviewer.handleKey(msg)
				require.NoError(t, <-done)
				require.Equal(t, int32(1), called.Load())
				_, err := caller(request)
				require.NoError(t, err)
				require.Equal(t, int32(2), called.Load())
				args["effect"] = "invalid"
				_, err = caller(proto.ToolCallRequest{Name: "process_run", Arguments: []byte(stringMustJSON(t, args))})
				require.ErrorContains(t, err, "effect")
				require.Equal(t, int32(2), called.Load())
				require.Empty(t, m.reviewer.rules.Snapshot())
				require.Equal(t, mode, m.Config.ReviewMode)
				next := newSessionToolReviewer(ctx, m.Config)
				require.Equal(t, ReviewNever, next.effectiveReviewMode())
				require.NoError(t, next.requestApproval(reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: AccessWrite, WriteCoverage: approval.CoverageIncomplete}}, "mcp_write", nil))
				require.Equal(t, mode, newSessionToolReviewer(WithReviewSession(context.Background()), m.Config).effectiveReviewMode())
				require.Equal(t, mode, newToolReviewer(m.Config).effectiveReviewMode())
			})
		}
	}
}

func TestFullControlRequiresExplicitChoice(t *testing.T) {
	for _, key := range []string{"y", "n", "esc"} {
		r := newPagedTestReviewer("echo hello")
		msg := tea.KeyPressMsg{Code: rune(key[0]), Text: key}
		if key == "esc" {
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		}
		r.handleKey(msg)
		require.False(t, r.fullControlState().Load())
	}
	r := newPagedTestReviewer("echo hello")
	r.reviewItem.name = "fs_write_file"
	r.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	require.False(t, r.fullControlState().Load())
	require.True(t, r.isPending())
}

func TestFullControlReleasesQueuedReview(t *testing.T) {
	r := newPagedTestReviewer("echo hello")
	r.handleKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	response := make(chan reviewResponse, 1)
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{name: "fs_write_file", resp: response}})
	require.Equal(t, reviewActionApprove, (<-response).action)
	require.False(t, r.isPending())
}
