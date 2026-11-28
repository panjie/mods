package app

import (
	"strings"
	"time"

	"github.com/panjie/mods/internal/proto"
)

// ToolStats summarizes tool execution counts and duration for an interaction.
type ToolStats struct {
	Total       int
	Rounds      int
	Succeeded   int
	NonZeroExit int
	Failed      int
	Denied      int
	Cancelled   int
	Duration    time.Duration
}

// InteractionStats captures token usage, timing, and execution counters for
// the most recent interaction on a Mods model.
type InteractionStats struct {
	Provider       string
	Model          string
	Usage          proto.TokenUsage
	Duration       time.Duration
	FirstResponse  time.Duration
	ModelDuration  time.Duration
	ReviewDuration time.Duration
	ModelRounds    int
	Retries        int
	Compactions    int
	Tools          ToolStats
}

// Stats returns a snapshot of interaction statistics accumulated on m.
func (m *Mods) Stats() InteractionStats {
	if m == nil {
		return InteractionStats{}
	}
	m.statsMu.Lock()
	defer m.statsMu.Unlock()

	duration := m.turnDuration
	if m.debugTurnActive && !m.debugTurnStarted.IsZero() {
		duration = time.Since(m.debugTurnStarted)
	}
	modelDuration := m.modelDuration
	if !m.debugRoundStarted.IsZero() {
		modelDuration += time.Since(m.debugRoundStarted)
	}

	return InteractionStats{
		Provider:       m.statsProvider,
		Model:          m.statsModel,
		Usage:          m.tokenUsage,
		Duration:       duration,
		FirstResponse:  m.firstResponse,
		ModelDuration:  modelDuration,
		ReviewDuration: m.reviewDuration,
		ModelRounds:    m.statsModelRounds,
		Retries:        m.statsRetries,
		Compactions:    m.statsCompactions,
		Tools: ToolStats{
			Total:       m.debugToolTotal,
			Rounds:      m.debugToolRounds,
			Succeeded:   m.debugToolSucceeded,
			NonZeroExit: m.debugToolExited,
			Failed:      m.debugToolFailed,
			Denied:      m.debugToolDenied,
			Cancelled:   m.debugToolCancelled,
			Duration:    m.toolDuration,
		},
	}
}

func (m *Mods) currentTurnRound() (int, int) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	return m.debugTurn, m.debugRound
}

func (m *Mods) setStatsModel(provider, model string) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.statsProvider = provider
	m.statsModel = model
}

func (m *Mods) recordFirstResponse(d time.Duration) {
	if d <= 0 {
		return
	}
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	if m.firstResponse == 0 {
		m.firstResponse = d
	}
}

func (m *Mods) recordRetry() {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.statsRetries++
}

func (m *Mods) recordCompaction() {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.statsCompactions++
}

func (m *Mods) recordToolCall(status string, execDuration, reviewWait time.Duration) {
	m.statsMu.Lock()
	defer m.statsMu.Unlock()
	m.debugToolTotal++
	switch {
	case status == "success":
		m.debugToolSucceeded++
	case strings.HasPrefix(status, "exit "):
		m.debugToolExited++
	case status == "denied":
		m.debugToolDenied++
	case status == "cancelled":
		m.debugToolCancelled++
	default:
		m.debugToolFailed++
	}
	m.toolDuration += execDuration
	m.reviewDuration += reviewWait
}
