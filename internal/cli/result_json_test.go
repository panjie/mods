package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/panjie/mods/internal/app"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestWriteResultJSONOnFailureIsAtomicPrivateAndSanitized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "result.json")
	withTestConfig(t, Config{ResultJSON: path, PersistentConfig: PersistentConfig{API: "openai", Model: "gpt-test"}}, func() {
		require.NoError(t, writeResultJSON(nil, errors.New("secret-token-must-not-appear")))
	})

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NotContains(t, string(data), "secret-token-must-not-appear")
	var report resultJSONReport
	require.NoError(t, json.Unmarshal(data, &report))
	require.Equal(t, resultSchemaVersion, report.SchemaVersion)
	require.Equal(t, "failed", string(report.Status))
	require.Equal(t, "provider_error", string(report.StopReason))
	require.Equal(t, "openai", report.Provider)
	require.Equal(t, "gpt-test", report.Model)

	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(path)
		require.NoError(t, statErr)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".mods-result-*.tmp"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestBuildResultJSONReportCompleted(t *testing.T) {
	started := time.Date(2026, 8, 16, 1, 2, 3, 0, time.UTC)
	result := app.TurnResult{
		Status: app.TurnStatusCompleted, StopReason: app.StopReasonModelComplete,
		StartedAt: started, FinishedAt: started.Add(2 * time.Second),
		Provider: "openai", Model: "gpt-test",
		Stats: app.TurnStats{ModelRounds: 2, ToolRounds: 1, ToolTotal: 3, ToolSucceeded: 3},
	}
	report := buildResultJSONReport(result, proto.TokenUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}, nil)
	require.Equal(t, app.TurnStatusCompleted, report.Status)
	require.Equal(t, app.StopReasonModelComplete, report.StopReason)
	require.Equal(t, int64(2000), report.DurationMS)
	require.Equal(t, 2, report.Rounds.Model)
	require.Equal(t, 1, report.Rounds.Tool)
	require.Equal(t, 3, report.Tools.Succeeded)
	require.True(t, report.Tokens.Available)
	require.Nil(t, report.Error)
}

func TestBuildResultJSONReportMarksPostRunFailure(t *testing.T) {
	now := time.Now().UTC()
	report := buildResultJSONReport(app.TurnResult{
		Status: app.TurnStatusCompleted, StopReason: app.StopReasonModelComplete,
		StartedAt: now, FinishedAt: now,
	}, proto.TokenUsage{}, errors.New("session save failed"))
	require.Equal(t, app.TurnStatusFailed, report.Status)
	require.Equal(t, app.StopReasonInternalError, report.StopReason)
}

func TestWriteResultJSONRejectsStdout(t *testing.T) {
	withTestConfig(t, Config{ResultJSON: "-"}, func() {
		require.Error(t, writeResultJSON(nil, nil))
	})
}
