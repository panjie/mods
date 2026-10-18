package evals

import (
	"strings"
	"unicode/utf8"

	"github.com/panjie/mods/internal/proto"
)

// ExecutionMetrics counts observable transcript events, not semantic quality.
// Missing usage/timing data is intentionally not estimated from text.
type ExecutionMetrics struct {
	UserInputCalls            int `json:"user_input_calls"`
	FailedToolResults         int `json:"failed_tool_results"`
	FailedUserInputResults    int `json:"failed_user_input_results"`
	SimplificationSuggestions int `json:"simplification_suggestions"`
	FinalAnswerCharacters     int `json:"final_answer_characters"`
}

func MeasureExecution(messages []proto.Message) ExecutionMetrics {
	var metrics ExecutionMetrics
	for _, message := range messages {
		switch message.Role {
		case proto.RoleAssistant:
			if len(message.ToolCalls) == 0 && strings.TrimSpace(message.Content) != "" {
				metrics.FinalAnswerCharacters = utf8.RuneCountInString(message.Content)
			}
			for _, call := range message.ToolCalls {
				if call.Function.Name == "request_user_input" {
					metrics.UserInputCalls++
				}
			}
		case proto.RoleTool:
			for _, call := range message.ToolCalls {
				if !call.IsError {
					continue
				}
				metrics.FailedToolResults++
				if call.Function.Name == "request_user_input" {
					// Includes cancellations/unavailable input; not necessarily
					// invalid arguments. Do not infer a cause from prose.
					metrics.FailedUserInputResults++
				}
				if strings.HasPrefix(message.Content, "command needs simplification:") {
					metrics.SimplificationSuggestions++
				}
			}
		}
	}
	return metrics
}
