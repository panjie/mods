package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCommandConstraintRepeatedCallsNeverExecute(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	cfg.BuiltinTools.Workspace = t.TempDir()
	m := &Mods{Config: &cfg, ctx: context.Background(), shellAnalyzer: func(string, string) approval.CommandAssessment {
		return approval.CommandAssessment{Effect: approval.EffectRead}
	}}
	registry := toolregistry.NewRegistry()
	var executed atomic.Int32
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call: func(context.Context, json.RawMessage) (string, error) { executed.Add(1); return "", nil },
	}))
	caller := m.toolCaller(registry, &cfg)
	for i := 0; i < 8; i++ {
		data, _ := json.Marshal(map[string]string{"command": fmt.Sprintf("mkdir d%d && touch d%d/a && ls", i, i)})
		_, err := caller(proto.ToolCallRequest{Name: "shell_run", Arguments: data})
		require.Error(t, err)
		if i >= 2 {
			require.ErrorIs(t, err, errCommandReviewability)
		}
	}
	require.Zero(t, executed.Load())
}

func TestCommandConstraintStaticReadsAndAdvisory(t *testing.T) {
	cfg := defaultConfig()
	gate := newCommandPreflightGate(&cfg)
	a := approval.CommandAssessment{Effect: approval.EffectRead, StaticRead: true, Shape: approval.CommandShape{TopLevelActions: 5, Opaque: true}}
	require.NoError(t, gate.check("shell_run", a))
	a.StaticRead = false
	require.Error(t, gate.check("shell_run", a))
	advice := approval.CommandAssessment{Effect: approval.EffectWrite, Reviewability: approval.CommandReviewability{ShouldCorrect: true, Reasons: []approval.ReviewabilityReason{approval.ReviewabilitySingleProgramInShell}}}
	require.Error(t, gate.check("shell_run", advice))
	require.NoError(t, gate.check("shell_run", advice))
	require.Error(t, gate.check("shell_run", a))
	require.ErrorIs(t, gate.check("shell_run", a), errCommandReviewability)
}

func TestFullReviewRequiresEveryPage(t *testing.T) {
	data, _ := json.Marshal(map[string]string{"interpreter": "sh", "source": strings.Repeat("echo '中文 text'\n", 35) + "printf '\\033[2J'\n# \x1b[2J", "cwd": "/tmp"})
	p := formatReviewPresentationWithIntent("script_run", data, approval.CommandAssessment{}, testApprovalScope, AccessIntent{})
	resp := make(chan reviewResponse, 1)
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{name: "script_run", presentation: p, resp: resp}})
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

func TestScriptReviewCannotUseSavedRulesOrTempExemption(t *testing.T) {
	r := &toolReviewer{reviewMode: ReviewAuto, scope: testApprovalScope, raw: true}
	deps := reviewerDeps{ctx: context.Background(), accessIntent: AccessIntent{Class: approval.AccessWrite, UncertainEffect: true}, safeDirs: []string{testApprovalScope.Value}}
	require.ErrorIs(t, r.requestApproval(deps, "script_run", []byte(`{"interpreter":"sh","source":"echo x"}`)), errReviewUnavailable)
	r.reviewMode = ReviewNever
	require.NoError(t, r.requestApproval(deps, "script_run", []byte(`{"interpreter":"sh","source":"echo x"}`)))
}

func TestCommandConstraintBudgetEndsTurnWithoutQuitting(t *testing.T) {
	m := &Mods{
		Config:       &Config{},
		reviewer:     &toolReviewer{},
		ctx:          context.Background(),
		Styles:       makeStyles(true),
		contentMutex: &sync.Mutex{},
	}
	runner := newStreamRunner(staticStream{}, nil, nil, func(err error) tea.Msg { return modsError{Err: err} })
	_, cmd := m.Update(streamEventMsg{kind: streamEventToolCalls, runner: runner, results: []proto.ToolCallStatus{{Name: "shell_run", Err: errCommandReviewability}}})
	require.True(t, runner.closed.Load())
	require.Nil(t, m.Error, "the exhausted budget must end the turn, not the session")
	require.Contains(t, m.Output, "Execution stopped")
	require.Contains(t, m.Output, "could not be made reviewable")

	event, ok := cmd().(streamEventMsg)
	require.True(t, ok)
	require.Equal(t, streamEventDone, event.kind)

	model, quitCmd := m.Update(event)
	require.Equal(t, doneState, model.(*Mods).state)
	require.IsType(t, quitMsg{}, quitCmd())
}
