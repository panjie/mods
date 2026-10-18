package evals

import (
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func result(name, text string, failed bool) proto.Message {
	return proto.Message{Role: proto.RoleTool, Content: text, ToolCalls: []proto.ToolCall{{
		ID: "call-" + name, Function: proto.Function{Name: name}, IsError: failed,
	}}}
}

func TestExecutionMetricsCountInvocationsNotResultMetadata(t *testing.T) {
	messages := []proto.Message{
		userMessage("Make this explicit edit"),
		assistantTool("shell_run", `{}`),
		result("shell_run", "command needs simplification: combines 2 top-level actions", true),
		assistantTool("request_user_input", `{}`),
		result("request_user_input", "question must be at most 160 characters", true),
		assistantTool("fs_replace", `{}`),
		result("fs_replace", "old_text was not found", true),
		assistantTool("fs_read_file", `{}`),
		result("fs_read_file", "current contents", false),
		assistantTool("fs_replace", `{}`),
		result("fs_replace", "Replaced 1 occurrence", false),
		assistantText("修改完成。"),
	}
	c, ok := CaseByID(CaseClearModification)
	require.True(t, ok)
	report := Score(messages, c)
	require.Equal(t, 5, report.ToolCalls)
	require.Equal(t, "shell_run", report.FirstTool)
	require.Equal(t, ExecutionMetrics{
		UserInputCalls: 1, FailedToolResults: 3, FailedUserInputResults: 1,
		SimplificationSuggestions: 1, FinalAnswerCharacters: 5,
	}, report.Metrics)
	require.False(t, findDimension(t, report, "no-unneeded-input").Passed)
	require.NotEmpty(t, report.Notes)
}

func TestExecutionCasesKeepClarificationAndVerificationDistinct(t *testing.T) {
	for _, id := range []string{CaseClearModification, CaseMissingModificationContext, CaseEditRecovery, CaseCheckRecovery, CaseVisualVerification} {
		t.Run(id, func(t *testing.T) {
			c, ok := CaseByID(id)
			require.True(t, ok)
			messages := []proto.Message{userMessage("request")}
			if c.RequiresClarification {
				messages = append(messages, assistantTool("request_user_input", `{}`), result("request_user_input", "target supplied", false))
			}
			messages = append(messages, assistantTool("fs_read_file", `{}`), result("fs_read_file", "file", false))
			if id == CaseCheckRecovery {
				messages = append(messages, assistantTool("process_run", `{}`), result("process_run", "check failed", true))
			}
			messages = append(messages, assistantTool("fs_replace", `{}`), result("fs_replace", "changed", false), assistantText("Done; live activation unverified."))
			r := Score(messages, c)
			require.True(t, r.Passed(), "%+v", r.Dimensions)
			require.NotEmpty(t, r.Notes, "mechanical scores must not claim semantic correctness")
		})
	}
}

func TestMetricsDoNotInferFailuresFromSuccessfulOutput(t *testing.T) {
	m := MeasureExecution([]proto.Message{
		assistantTool("fs_read_file", `{}`),
		result("fs_read_file", "command needs simplification: quoted fixture text", false),
	})
	require.Zero(t, m.FailedToolResults)
	require.Zero(t, m.SimplificationSuggestions)
}
