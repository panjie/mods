package app

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func complexReviewabilityAnalysis() approval.CommandAssessment {
	return approval.CommandAssessment{
		Shape: approval.CommandShape{TopLevelActions: 4},
		Reviewability: approval.CommandReviewability{
			Level:         approval.ReviewabilityCompound,
			Reasons:       []approval.ReviewabilityReason{approval.ReviewabilityMultipleIndependent},
			ShouldCorrect: true,
		},
	}
}

func TestCommandPreflightGateCorrectsAtMostOnce(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	gate := newCommandPreflightGate(&cfg)
	first := gate.check("powershell_run", complexReviewabilityAnalysis())
	require.Error(t, first)
	var correction correctionSuggester
	require.True(t, errors.As(first, &correction))
	require.True(t, correction.CorrectionSuggested())
	require.Contains(t, first.Error(), "4 top-level actions")

	require.NoError(t, gate.check("powershell_run", complexReviewabilityAnalysis()))
	require.NoError(t, gate.check("process_run", complexReviewabilityAnalysis()))
}

func TestCommandPreflightGateModes(t *testing.T) {
	minimal := defaultConfig()
	minimal.Minimal = true
	never := defaultConfig()
	never.ReviewMode = ReviewNever
	for _, cfg := range []*Config{&minimal, &never} {
		gate := newCommandPreflightGate(cfg)
		require.NoError(t, gate.check("shell_run", complexReviewabilityAnalysis()))
	}
}

func TestCommandPreflightGateConcurrentBudget(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	gate := newCommandPreflightGate(&cfg)
	var corrections atomic.Int32
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if gate.check("shell_run", complexReviewabilityAnalysis()) != nil {
				corrections.Add(1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), corrections.Load())
}

func TestCommandSimplificationMessageDoesNotEchoTargets(t *testing.T) {
	assessment := approval.CommandAssessment{
		DynamicTargets: []string{"$SECRET_PROFILE"},
		Reviewability: approval.CommandReviewability{
			Level:         approval.ReviewabilityCompound,
			Reasons:       []approval.ReviewabilityReason{approval.ReviewabilityDynamicWriteTarget},
			ShouldCorrect: true,
		},
	}
	message := commandSimplificationMessage(assessment)
	require.Contains(t, message, "runtime-resolved path")
	require.NotContains(t, message, "$SECRET_PROFILE")
}

func TestToolCallerCorrectionDoesNotExecuteCommand(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewAuto
	cfg.BuiltinTools.Workspace = t.TempDir()
	m := &Mods{Config: &cfg, ctx: context.Background()}

	registry := toolregistry.NewRegistry()
	var executed atomic.Int32
	require.NoError(t, registry.Register(toolregistry.Tool{
		Kind:         toolregistry.ToolKindShell,
		Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Spec: proto.ToolSpec{
			Name: "shell_run",
			InputSchema: map[string]any{
				"type":     "object",
				"required": []string{"command"},
				"properties": map[string]any{
					"command": map[string]any{"type": "string"},
				},
			},
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			executed.Add(1)
			return "unexpected", nil
		},
	}))

	_, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{ID: "call-1", Index: 1, Total: 1, Name: "shell_run", Arguments: []byte(`{"command":"rm out.txt"}`)})
	require.Error(t, err)
	require.Contains(t, err.Error(), "process_run")
	require.Zero(t, executed.Load(), "preflight correction must happen before tool execution")
}

func TestReadOnlyProcessRecommendationDoesNotConsumeCorrection(t *testing.T) {
	cfg := defaultConfig()
	m := &Mods{Config: &cfg}
	analysis := m.assessCommand("shell_run", "git status")
	require.Equal(t, "process_run", analysis.Reviewability.RecommendedTool)
	require.False(t, analysis.Reviewability.ShouldCorrect)

	gate := newCommandPreflightGate(&cfg)
	require.NoError(t, gate.check("shell_run", analysis))
	require.Error(t, gate.check("shell_run", complexReviewabilityAnalysis()), "the correction budget must remain available for a compound call")
}

func TestNestedShellProcessRequestsCorrection(t *testing.T) {
	cfg := defaultConfig()
	gate := newCommandPreflightGate(&cfg)
	analysis := approval.CommandAssessment{Reviewability: approval.AnalyzeProcessReviewability("pwsh", []string{"-Command", "Get-Date"}, false)}
	err := gate.check("process_run", analysis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "powershell_run")
}

func TestKnownCommandPassedAsShellScriptRequestsDirectProcess(t *testing.T) {
	cfg := defaultConfig()
	gate := newCommandPreflightGate(&cfg)
	analysis := approval.CommandAssessment{Reviewability: approval.AnalyzeProcessReviewability(
		"sh", []string{"head", "-80", "internal/app/status_flags.go"}, true,
	)}
	err := gate.check("process_run", analysis)
	require.Error(t, err)
	require.Contains(t, err.Error(), "known executable name")
	require.Contains(t, err.Error(), "Retry with process_run and literal argv")
}
