package app

import (
	"fmt"
	"strings"
	"time"
)

func (m *Mods) beginTurn(inputChars int) {
	m.statsMu.Lock()
	if m.debugTurnActive {
		m.statsMu.Unlock()
		return
	}
	m.debugTurn++
	m.debugRound = 1
	m.debugTurnStarted = time.Now()
	m.debugTurnActive = true
	m.debugRoundStarted = time.Time{}
	m.statsProvider = ""
	m.statsModel = ""
	m.turnDuration = 0
	m.firstResponse = 0
	m.modelDuration = 0
	m.reviewDuration = 0
	m.toolDuration = 0
	m.statsModelRounds = 0
	m.statsRetries = 0
	m.statsCompactions = 0
	m.debugToolTotal = 0
	m.debugToolRounds = 0
	m.debugToolSucceeded = 0
	m.debugToolExited = 0
	m.debugToolFailed = 0
	m.debugToolDenied = 0
	m.debugToolCancelled = 0
	turn := m.debugTurn
	m.statsMu.Unlock()

	if debug.Enabled() {
		debug.Print(DebugSection{Title: fmt.Sprintf("turn %d · start", turn), Fields: []DebugField{
			{Label: "input", Value: fmt.Sprintf("%d chars", inputChars)},
		}})
	}
}

func (m *Mods) startModelRound() {
	m.statsMu.Lock()
	if !m.debugTurnActive {
		m.statsMu.Unlock()
		return
	}
	m.debugRoundStarted = time.Now()
	if m.debugRound > m.statsModelRounds {
		m.statsModelRounds = m.debugRound
	}
	turn := m.debugTurn
	round := m.debugRound
	m.statsMu.Unlock()

	m.debugThoughtStart = len([]rune(m.Thought))
	m.debugActivities = nil
	if debug.Enabled() {
		debug.Print(DebugSection{
			Title: fmt.Sprintf("model · turn %d · round %d · start", turn, round),
		})
	}
}

func (m *Mods) endModelRound(outcome string, toolCalls int, err error) {
	m.statsMu.Lock()
	if m.debugRoundStarted.IsZero() {
		m.statsMu.Unlock()
		return
	}
	elapsed := time.Since(m.debugRoundStarted)
	m.modelDuration += elapsed
	m.debugRoundStarted = time.Time{}
	turn := m.debugTurn
	round := m.debugRound
	m.statsMu.Unlock()

	if !debug.Enabled() {
		return
	}
	fields := []DebugField{
		{Label: "outcome", Value: outcome},
		{Label: "duration", Value: formatDebugDuration(elapsed)},
	}
	if toolCalls > 0 {
		fields = append(fields, DebugField{Label: "tools", Value: fmt.Sprintf("%d call(s)", toolCalls)})
	}
	if thought := len([]rune(m.Thought)) - m.debugThoughtStart; thought > 0 {
		disposition := "discarded"
		if m.thinkActive {
			disposition = "displayed"
		}
		fields = append(fields, DebugField{Label: "thinking", Value: fmt.Sprintf("%d chars · %s", thought, disposition)})
	}
	if len(m.debugActivities) > 0 {
		fields = append(fields, DebugField{Label: "activity", Value: strings.Join(m.debugActivities, " → ")})
	}
	if err != nil {
		fields = append(fields, DebugField{Label: "error", Value: err.Error()})
	}
	debug.Print(DebugSection{
		Title:  fmt.Sprintf("model · turn %d · round %d · %s", turn, round, outcome),
		Fields: fields,
	})
}

func (m *Mods) recordToolRoundDone(calls int) {
	m.statsMu.Lock()
	m.debugToolRounds++
	turn := m.debugTurn
	round := m.debugRound
	m.debugRound++
	m.statsMu.Unlock()

	if debug.Enabled() {
		debug.Print(DebugSection{Title: fmt.Sprintf("tool · turn %d · round %d · complete", turn, round), Fields: []DebugField{
			{Label: "calls", Value: fmt.Sprintf("%d", calls)},
		}})
	}
}

func (m *Mods) endTurn(status string, err error) {
	m.statsMu.Lock()
	if !m.debugTurnActive {
		m.statsMu.Unlock()
		return
	}
	if !m.debugRoundStarted.IsZero() {
		m.modelDuration += time.Since(m.debugRoundStarted)
		m.debugRoundStarted = time.Time{}
	}
	m.turnDuration = time.Since(m.debugTurnStarted)
	m.debugTurnActive = false
	turn := m.debugTurn
	duration := m.turnDuration
	round := m.debugRound
	toolRounds := m.debugToolRounds
	toolTotal := m.debugToolTotal
	toolSucceeded := m.debugToolSucceeded
	toolExited := m.debugToolExited
	toolFailed := m.debugToolFailed
	toolDenied := m.debugToolDenied
	toolCancelled := m.debugToolCancelled
	m.statsMu.Unlock()

	if !debug.Enabled() {
		return
	}
	fields := []DebugField{
		{Label: "status", Value: status},
		{Label: "duration", Value: formatDebugDuration(duration)},
		{Label: "rounds", Value: fmt.Sprintf("model %d · tool %d", round, toolRounds)},
		{Label: "tools", Value: fmt.Sprintf("%d total · %d success · %d non-zero · %d failed · %d denied · %d cancelled", toolTotal, toolSucceeded, toolExited, toolFailed, toolDenied, toolCancelled)},
	}
	if m.tokenUsage.Available() {
		fields = append(fields, DebugField{Label: "tokens", Value: fmt.Sprintf("input=%d · cached=%d · output=%d · reasoning=%d · total=%d", m.tokenUsage.InputTokens, m.tokenUsage.CachedInputTokens, m.tokenUsage.OutputTokens, m.tokenUsage.ReasoningOutputTokens, m.tokenUsage.TotalTokens)})
	}
	if err != nil {
		fields = append(fields, DebugField{Label: "error", Value: err.Error()})
	}
	debug.Print(DebugSection{Title: fmt.Sprintf("turn %d · %s", turn, status), Fields: fields})
}
