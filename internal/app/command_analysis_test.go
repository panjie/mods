package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCommandAdapterErrorsRemainUnapprovableByRules(t *testing.T) {
	cwd := canonicalTestPath(t, t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name, tool, input string
		ctx               context.Context
	}{
		{"malformed JSON", "process_run", `{`, context.Background()},
		{"empty program", "process_run", `{"program":""}`, context.Background()},
		{"cancelled shell", "shell_run", "cat README.md", ctx},
		{"cancelled process", "process_run", `{"program":"cat","args":["README.md"]}`, ctx},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &Mods{Config: testConfigForWorkingDir(cwd), ctx: tc.ctx}
			result := m.assessCommand(tc.tool, tc.input)
			require.Equal(t, approval.EffectUnknown, result.Effect)
			intent := commandResultToIntent(commandanalysis.Result(result))
			require.Equal(t, approval.AccessWrite, intent.Class)
			require.Equal(t, approval.CoverageIncomplete, intent.WriteCoverage)
			require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(cwd), []string{cwd}, ApprovalReviewMode(ReviewAuto)))
			rules := []ApprovalRule{{Type: approval.DirAllow, Paths: []string{cwd}, Mode: AccessWrite}}
			require.False(t, RulesAllowIntent(rules, intent, WorkingDirScope(cwd), []string{cwd}, ApprovalReviewMode(ReviewAuto)))
		})
	}
}

func TestCommandResultAdapterRequiresReadEvidence(t *testing.T) {
	for _, result := range []commandanalysis.Result{{}, {Effect: commandanalysis.EffectRead}, commandanalysis.UnknownCommandAssessment()} {
		intent := commandResultToIntent(result)
		require.Equal(t, approval.AccessWrite, intent.Class)
		require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(t.TempDir()), nil, ApprovalReviewMode(ReviewAuto)))
	}
}

func TestCancelledCommandAnalysisNeverDispatches(t *testing.T) {
	for _, mode := range []ReviewMode{ReviewAuto, ReviewAlways, ReviewNever} {
		t.Run(string(mode), func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.ReviewMode = mode
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
			registry := toolregistry.NewRegistry()
			calls := 0
			require.NoError(t, registry.Register(toolregistry.Tool{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, Call: func(context.Context, json.RawMessage) (string, error) { calls++; return "", nil }}))
			_, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{Name: "shell_run", Arguments: []byte(`{"command":"cat README.md"}`)})
			require.ErrorIs(t, err, context.Canceled)
			require.Zero(t, calls)
		})
	}
}
