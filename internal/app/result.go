package app

import "time"

// TurnStatus describes the terminal state of one model interaction.
type TurnStatus string

const (
	TurnStatusRunning    TurnStatus = "running"
	TurnStatusCompleted  TurnStatus = "completed"
	TurnStatusIncomplete TurnStatus = "incomplete"
	TurnStatusFailed     TurnStatus = "failed"
	TurnStatusCancelled  TurnStatus = "cancelled"
)

// StopReason is a stable, machine-readable explanation for why a turn ended.
type StopReason string

const (
	StopReasonModelComplete       StopReason = "model_complete"
	StopReasonProviderError       StopReason = "provider_error"
	StopReasonProviderIdleTimeout StopReason = "provider_idle_timeout"
	StopReasonToolRoundLimit      StopReason = "tool_round_limit"
	StopReasonFailedToolLimit     StopReason = "failed_tool_round_limit"
	StopReasonCancelled           StopReason = "cancelled"
	StopReasonInternalError       StopReason = "internal_error"
)

// TurnStats contains provider-neutral counters collected regardless of debug mode.
type TurnStats struct {
	ModelRounds   int `json:"model_rounds"`
	ToolRounds    int `json:"tool_rounds"`
	ToolTotal     int `json:"tool_total"`
	ToolSucceeded int `json:"tool_succeeded"`
	ToolExited    int `json:"tool_non_zero"`
	ToolFailed    int `json:"tool_failed"`
	ToolDenied    int `json:"tool_denied"`
	ToolCorrected int `json:"tool_corrected"`
	ToolCancelled int `json:"tool_cancelled"`
	Retries       int `json:"retries"`
}

// TurnResult is the application-level outcome of one interaction.
type TurnResult struct {
	Status      TurnStatus `json:"status"`
	StopReason  StopReason `json:"stop_reason,omitempty"`
	StartedAt   time.Time  `json:"started_at"`
	FinishedAt  time.Time  `json:"finished_at,omitempty"`
	Provider    string     `json:"provider,omitempty"`
	Model       string     `json:"model,omitempty"`
	ErrorType   string     `json:"-"`
	ErrorReason string     `json:"-"`
	Stats       TurnStats  `json:"stats"`
}

func (m *Mods) startTurnResult() {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	if m.turnResult.Status == TurnStatusRunning {
		m.turnResult.Stats.ModelRounds++
		return
	}
	m.turnResult = TurnResult{
		Status:    TurnStatusRunning,
		StartedAt: time.Now().UTC(),
		Stats:     TurnStats{ModelRounds: 1},
	}
}

func (m *Mods) finishTurnResult(status TurnStatus, reason StopReason, errType, errReason string) {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	if m.turnResult.StartedAt.IsZero() {
		m.turnResult.StartedAt = time.Now().UTC()
	}
	// Preserve a more specific incomplete outcome when the synthetic done
	// event used to stop the Bubble Tea loop arrives afterwards.
	if m.turnResult.Status == TurnStatusIncomplete && status == TurnStatusCompleted {
		return
	}
	m.turnResult.Status = status
	m.turnResult.StopReason = reason
	m.turnResult.ErrorType = errType
	m.turnResult.ErrorReason = errReason
	m.turnResult.Stats.Retries = m.retries
	m.turnResult.FinishedAt = time.Now().UTC()
}

// Result returns a copy of the current turn outcome.
func (m *Mods) Result() TurnResult {
	if m == nil {
		return TurnResult{}
	}
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	result := m.turnResult
	result.Stats.Retries = m.retries
	return result
}

func (m *Mods) incrementModelRounds() {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	m.turnResult.Stats.ModelRounds++
}

func (m *Mods) incrementToolRounds() {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	m.turnResult.Stats.ToolRounds++
}

func (m *Mods) setResultModel(provider, model string) {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	m.turnResult.Provider = provider
	m.turnResult.Model = model
}

func (m *Mods) recordToolResult(status string) {
	m.resultMu.Lock()
	defer m.resultMu.Unlock()
	m.turnResult.Stats.ToolTotal++
	switch {
	case status == "success":
		m.turnResult.Stats.ToolSucceeded++
	case len(status) >= len("exit ") && status[:len("exit ")] == "exit ":
		m.turnResult.Stats.ToolExited++
	case status == "denied":
		m.turnResult.Stats.ToolDenied++
	case status == "correction":
		m.turnResult.Stats.ToolCorrected++
	case status == "cancelled":
		m.turnResult.Stats.ToolCancelled++
	default:
		m.turnResult.Stats.ToolFailed++
	}
}
