package app

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestModsStatsNilAndZero(t *testing.T) {
	var nilMods *Mods
	require.Equal(t, InteractionStats{}, nilMods.Stats())
	require.Equal(t, InteractionStats{}, (&Mods{}).Stats())
}

func TestModsStatsLifecycleWithoutDebug(t *testing.T) {
	debugpkg.SetEnabled(false)

	m := testMods(t)
	m.beginTurn(12)
	m.setStatsModel("openai", "gpt-5")
	m.startModelRound()
	time.Sleep(15 * time.Millisecond)
	m.recordFirstResponse(10 * time.Millisecond)

	live := m.Stats()
	require.Equal(t, "openai", live.Provider)
	require.Equal(t, "gpt-5", live.Model)
	require.Equal(t, 1, live.ModelRounds)
	require.Equal(t, 10*time.Millisecond, live.FirstResponse)
	require.GreaterOrEqual(t, live.Duration, 10*time.Millisecond)
	require.GreaterOrEqual(t, live.ModelDuration, 10*time.Millisecond)

	m.endModelRound("response received", 1, nil)
	m.recordToolRoundDone(1)
	m.startModelRound()
	time.Sleep(10 * time.Millisecond)
	m.recordRetry()
	m.recordCompaction()
	m.tokenUsage.Add(proto.TokenUsage{InputTokens: 50, OutputTokens: 25, TotalTokens: 75})
	m.endTurn("complete", nil)

	final := m.Stats()
	require.Equal(t, "openai", final.Provider)
	require.Equal(t, "gpt-5", final.Model)
	require.Equal(t, 2, final.ModelRounds)
	require.Equal(t, 1, final.Tools.Rounds)
	require.Equal(t, 1, final.Retries)
	require.Equal(t, 1, final.Compactions)
	require.Equal(t, proto.TokenUsage{InputTokens: 50, OutputTokens: 25, TotalTokens: 75}, final.Usage)
	require.GreaterOrEqual(t, final.ModelDuration, 20*time.Millisecond)
	require.GreaterOrEqual(t, final.Duration, final.ModelDuration)

	// Calling beginTurn again starts a fresh turn and resets per-turn counters.
	m.beginTurn(5)
	reset := m.Stats()
	require.Empty(t, reset.Provider)
	require.Empty(t, reset.Model)
	require.Zero(t, reset.ModelRounds)
	require.Zero(t, reset.Retries)
	require.Zero(t, reset.Compactions)
	require.Zero(t, reset.FirstResponse)
}

func TestModsStatsRecordsToolOutcomesAndReviewWait(t *testing.T) {
	debugpkg.SetEnabled(false)

	cfg := defaultConfig()
	cfg.MCPTimeout = time.Second
	cfg.ReviewMode = ReviewAuto
	cfg.InteractiveTTYAvailable = true

	reviewer := newToolReviewer(&cfg)
	reviewer.reviewChan = make(chan toolReviewItem, 1)

	m := &Mods{
		Config:   &cfg,
		ctx:      context.Background(),
		reviewer: reviewer,
		secrets:  secrets.New(),
	}
	m.beginTurn(10)

	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "ok_read", InputSchema: map[string]any{"type": "object"}},
		Capabilities: toolregistry.ToolCapabilities{
			ReadOnly: true,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			time.Sleep(5 * time.Millisecond)
			return "ok", nil
		},
	}))
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "exit_read", InputSchema: map[string]any{"type": "object"}},
		Capabilities: toolregistry.ToolCapabilities{
			ReadOnly: true,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			return "", toolregistry.ShellExitError{Code: 2}
		},
	}))
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "fail_read", InputSchema: map[string]any{"type": "object"}},
		Capabilities: toolregistry.ToolCapabilities{
			ReadOnly: true,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			return "", errors.New("boom")
		},
	}))
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "write_op", InputSchema: map[string]any{"type": "object"}},
		Capabilities: toolregistry.ToolCapabilities{
			ReadOnly: false,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			time.Sleep(10 * time.Millisecond)
			return "written", nil
		},
	}))

	caller := m.toolCaller(registry, &cfg)

	_, err := caller(proto.ToolCallRequest{ID: "1", Index: 1, Total: 4, Name: "ok_read", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	_, err = caller(proto.ToolCallRequest{ID: "2", Index: 2, Total: 4, Name: "exit_read", Arguments: []byte(`{}`)})
	require.Error(t, err)
	_, err = caller(proto.ToolCallRequest{ID: "3", Index: 3, Total: 4, Name: "fail_read", Arguments: []byte(`{}`)})
	require.Error(t, err)

	// Approve one write_op after a simulated review wait, then deny a second.
	doneReview := make(chan struct{})
	go func() {
		defer close(doneReview)
		item := <-reviewer.reviewChan
		time.Sleep(25 * time.Millisecond)
		item.resp <- reviewResponse{action: reviewActionApprove}

		item = <-reviewer.reviewChan
		time.Sleep(20 * time.Millisecond)
		item.resp <- reviewResponse{action: reviewActionDeny}
	}()

	_, err = caller(proto.ToolCallRequest{ID: "4", Index: 4, Total: 5, Name: "write_op", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	_, err = caller(proto.ToolCallRequest{ID: "5", Index: 5, Total: 5, Name: "write_op", Arguments: []byte(`{}`)})
	require.Error(t, err)
	<-doneReview

	m.endTurn("complete", nil)
	stats := m.Stats()
	require.Equal(t, 5, stats.Tools.Total)
	require.Equal(t, 2, stats.Tools.Succeeded)
	require.Equal(t, 1, stats.Tools.NonZeroExit)
	require.Equal(t, 1, stats.Tools.Failed)
	require.Equal(t, 1, stats.Tools.Denied)
	require.Zero(t, stats.Tools.Cancelled)
	require.GreaterOrEqual(t, stats.ReviewDuration, 40*time.Millisecond)
	require.GreaterOrEqual(t, stats.Tools.Duration, 10*time.Millisecond)
}
