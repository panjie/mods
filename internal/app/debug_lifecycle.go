package app

import (
	"fmt"
	"strings"
	"time"
)

func (m *Mods) debugStartModelRound() {
	if !debug.Enabled() || !m.debugTurnActive {
		return
	}
	m.debugRoundStarted = time.Now()
	m.debugThoughtStart = len([]rune(m.Thought))
	m.debugActivities = nil
	debug.Print(DebugSection{
		Title: fmt.Sprintf("model · turn %d · round %d · start", m.debugTurn, m.debugRound),
	})
}

func (m *Mods) debugEndModelRound(outcome string, toolCalls int, err error) {
	if !debug.Enabled() || m.debugRoundStarted.IsZero() {
		return
	}
	fields := []DebugField{
		{Label: "outcome", Value: outcome},
		{Label: "duration", Value: formatDebugDuration(time.Since(m.debugRoundStarted))},
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
		Title:  fmt.Sprintf("model · turn %d · round %d · %s", m.debugTurn, m.debugRound, outcome),
		Fields: fields,
	})
	m.debugRoundStarted = time.Time{}
}

func (m *Mods) debugEndTurn(status string, err error) {
	if !debug.Enabled() || !m.debugTurnActive {
		return
	}
	fields := []DebugField{
		{Label: "status", Value: status},
		{Label: "duration", Value: formatDebugDuration(time.Since(m.debugTurnStarted))},
		{Label: "rounds", Value: fmt.Sprintf("model %d · tool %d", m.debugRound, m.debugToolRounds)},
		{Label: "tools", Value: fmt.Sprintf("%d total · %d success · %d non-zero · %d failed · %d denied · %d corrected · %d cancelled", m.debugToolTotal, m.debugToolSucceeded, m.debugToolExited, m.debugToolFailed, m.debugToolDenied, m.debugToolCorrected, m.debugToolCancelled)},
	}
	if m.tokenUsage.Available() {
		fields = append(fields, DebugField{Label: "tokens", Value: fmt.Sprintf("input=%d · cached=%d · output=%d · reasoning=%d · total=%d", m.tokenUsage.InputTokens, m.tokenUsage.CachedInputTokens, m.tokenUsage.OutputTokens, m.tokenUsage.ReasoningOutputTokens, m.tokenUsage.TotalTokens)})
	}
	if err != nil {
		fields = append(fields, DebugField{Label: "error", Value: err.Error()})
	}
	debug.Print(DebugSection{Title: fmt.Sprintf("turn %d · %s", m.debugTurn, status), Fields: fields})
	m.debugTurnActive = false
	m.debugRoundStarted = time.Time{}
}
