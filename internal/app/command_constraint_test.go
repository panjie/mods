package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// The preflight is advisory: a command that keeps its shape is nudged a bounded
// number of times and then handed to ordinary approval. Nothing is stopped, and
// no command text is rejected on the gate's own authority.
func TestCommandConstraintNudgesThenDefersToApproval(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	cfg.WorkingDir = t.TempDir()
	m := &Mods{
		Config:   &cfg,
		ctx:      context.Background(),
		reviewer: &toolReviewer{reviewMode: ReviewAuto, scope: WorkingDirScope(cfg.WorkingDir), raw: true},
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.UnknownCommandAssessment()
		},
	}
	registry := toolregistry.NewRegistry()
	var executed atomic.Int32
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { executed.Add(1); return "", nil },
	}))
	caller := m.toolCaller(registry, &cfg)
	data, _ := json.Marshal(map[string]string{"command": "mkdir out && touch out/a && ls"})

	for i := 0; i < commandCorrectionBudget; i++ {
		_, err := caller(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
		var correction correctionSuggester
		require.Error(t, err)
		require.True(t, errors.As(err, &correction), "nudge %d must be advisory feedback for the model", i)
	}
	_, err := caller(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
	require.ErrorIs(t, err, errReviewUnavailable, "the exhausted budget defers to approval instead of stopping the call")
	require.Zero(t, executed.Load(), "no interactive approval channel means the command does not run")
}

func TestCommandConstraintStaticReadsSkipNudges(t *testing.T) {
	cfg := defaultConfig()
	gate := newCommandPreflightGate(&cfg)
	read := approval.CommandAssessment{Effect: approval.EffectRead, StaticRead: true, Shape: approval.CommandShape{TopLevelActions: 5, Opaque: true}}
	require.NoError(t, gate.check("shell_run", read))

	compound := approval.CommandAssessment{
		Effect:        approval.EffectWrite,
		Shape:         approval.CommandShape{TopLevelActions: 3, Opaque: true},
		Reviewability: approval.CommandReviewability{Level: approval.ReviewabilityOpaque},
	}
	require.Error(t, gate.check("shell_run", compound))
	require.Error(t, gate.check("shell_run", compound))
	require.NoError(t, gate.check("shell_run", compound), "the budget only limits nudges, never execution")

	scriptFile := compound
	scriptFile.Reviewability.ScriptFilePayload = true
	require.NoError(t, newCommandPreflightGate(&cfg).check("shell_run", scriptFile),
		"a pre-written script payload is executed as-is, without nudges")
}

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

func TestUncertainEffectReviewCannotUseSavedRulesOrTempExemption(t *testing.T) {
	r := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope, raw: true}
	deps := reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: approval.AccessWrite, UncertainEffect: true}, safeDirs: []string{testApprovalScope.Value}}
	require.ErrorIs(t, r.requestApproval(deps, "shell_run", []byte(`{"command":"echo x"}`)), errReviewUnavailable)
	r.reviewMode = ReviewNever
	require.NoError(t, r.requestApproval(deps, "shell_run", []byte(`{"command":"echo x"}`)))
}

func TestCommandConstraintBudgetKeepsTurnRunning(t *testing.T) {
	m := &Mods{
		Config:       &Config{},
		reviewer:     &toolReviewer{},
		ctx:          context.Background(),
		Styles:       makeStyles(true),
		contentMutex: &sync.Mutex{},
	}
	runner := newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return modsError{Err: err} })
	correction := commandSimplificationError{message: "command needs simplification: combines 3 top-level actions. "}
	_, cmd := m.Update(streamEventMsg{kind: streamEventToolCalls, runner: runner, results: []proto.ToolCallStatus{{Name: "shell_run", Err: correction}}})
	require.False(t, runner.closed.Load(), "an advisory nudge must not close the runner")
	require.Nil(t, m.Error)
	require.NotContains(t, m.Output, "Execution stopped")
	require.NotNil(t, cmd)
}
