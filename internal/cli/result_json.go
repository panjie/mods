package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/panjie/mods/internal/app"
	"github.com/panjie/mods/internal/proto"
)

const resultSchemaVersion = 1

type resultJSONReport struct {
	SchemaVersion int            `json:"schema_version"`
	Status        app.TurnStatus `json:"status"`
	StopReason    app.StopReason `json:"stop_reason,omitempty"`
	StartedAt     time.Time      `json:"started_at"`
	FinishedAt    time.Time      `json:"finished_at"`
	DurationMS    int64          `json:"duration_ms"`
	Provider      string         `json:"provider,omitempty"`
	Model         string         `json:"model,omitempty"`
	Rounds        resultRounds   `json:"rounds"`
	Tools         resultTools    `json:"tools"`
	Retries       int            `json:"retries"`
	Tokens        resultTokens   `json:"tokens"`
	Error         *resultError   `json:"error,omitempty"`
}

type resultRounds struct {
	Model int `json:"model"`
	Tool  int `json:"tool"`
}

type resultTools struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	NonZero   int `json:"non_zero"`
	Failed    int `json:"failed"`
	Denied    int `json:"denied"`
	Corrected int `json:"corrected"`
	Cancelled int `json:"cancelled"`
}

type resultTokens struct {
	Available       bool  `json:"available"`
	Input           int64 `json:"input"`
	CachedInput     int64 `json:"cached_input"`
	Output          int64 `json:"output"`
	ReasoningOutput int64 `json:"reasoning_output"`
	Total           int64 `json:"total"`
}

type resultError struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
}

func writeResultJSON(mods *Mods, runErr error) error {
	if config.ResultJSON == "" {
		return nil
	}
	if config.ResultJSON == "-" {
		return modsError{Err: newUserErrorf("--result-json requires a file path"), ReasonText: "Could not write result JSON."}
	}

	result := app.TurnResult{}
	if mods != nil {
		result = mods.Result()
	}
	usage := modsTokenUsage(mods)
	report := buildResultJSONReport(result, usage, runErr)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal result JSON: %w", err)
	}
	data = append(data, '\n')
	if err := writeResultFileAtomic(config.ResultJSON, data); err != nil {
		return modsError{Err: err, ReasonText: "Could not write result JSON."}
	}
	return nil
}

func buildResultJSONReport(result app.TurnResult, usage proto.TokenUsage, runErr error) resultJSONReport {
	if result.Status == "" || result.Status == app.TurnStatusRunning {
		result.Status = app.TurnStatusFailed
		result.StopReason = app.StopReasonProviderError
		result.FinishedAt = time.Now().UTC()
		if result.StartedAt.IsZero() {
			result.StartedAt = result.FinishedAt
		}
	}
	if runErr != nil && result.Status == app.TurnStatusCompleted {
		result.Status = app.TurnStatusFailed
		result.StopReason = app.StopReasonInternalError
		result.ErrorType = "internal"
		result.ErrorReason = "Post-run finalization failed."
	}
	if runErr != nil && result.ErrorReason == "" {
		result.ErrorType = "request"
		result.ErrorReason = "The run failed."
	}
	if result.Provider == "" {
		result.Provider = config.API
	}
	if result.Model == "" {
		result.Model = config.Model
	}

	report := resultJSONReport{
		SchemaVersion: resultSchemaVersion,
		Status:        result.Status,
		StopReason:    result.StopReason,
		StartedAt:     result.StartedAt,
		FinishedAt:    result.FinishedAt,
		DurationMS:    max(result.FinishedAt.Sub(result.StartedAt).Milliseconds(), 0),
		Provider:      result.Provider,
		Model:         result.Model,
		Rounds:        resultRounds{Model: result.Stats.ModelRounds, Tool: result.Stats.ToolRounds},
		Tools: resultTools{
			Total: result.Stats.ToolTotal, Succeeded: result.Stats.ToolSucceeded,
			NonZero: result.Stats.ToolExited, Failed: result.Stats.ToolFailed,
			Denied: result.Stats.ToolDenied, Corrected: result.Stats.ToolCorrected,
			Cancelled: result.Stats.ToolCancelled,
		},
		Retries: result.Stats.Retries,
		Tokens: resultTokens{
			Available: usage.Available(), Input: usage.InputTokens,
			CachedInput: usage.CachedInputTokens, Output: usage.OutputTokens,
			ReasoningOutput: usage.ReasoningOutputTokens, Total: usage.TotalTokens,
		},
	}
	if result.ErrorType != "" || result.ErrorReason != "" {
		report.Error = &resultError{Type: result.ErrorType, Reason: result.ErrorReason}
	}
	return report
}

func modsTokenUsage(mods *Mods) proto.TokenUsage {
	if mods == nil {
		return proto.TokenUsage{}
	}
	return mods.TokenUsage()
}

func writeResultFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".mods-result-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary result file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("set result file permissions: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write result file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close result file: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("replace result file: %w", err)
	}
	return nil
}
