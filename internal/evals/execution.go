package evals

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/panjie/mods/internal/proto"
)

// ExecutionMetrics counts observable transcript events, not semantic quality.
// Missing usage/timing data is intentionally not estimated from text.
type ExecutionMetrics struct {
	// Nil means the transcript lacks trusted dispatch/control events. Never
	// infer these counts from arbitrary tool output containing control JSON.
	AutomaticReorganizations *int `json:"automatic_reorganizations,omitempty"`
	ManualReorganizations    *int `json:"manual_reorganizations,omitempty"`
	ExecutionFailures        *int `json:"execution_failures,omitempty"`
	UserInputCalls           int  `json:"user_input_calls"`
	FailedToolResults        int  `json:"failed_tool_results"`
	FailedUserInputResults   int  `json:"failed_user_input_results"`
	// Retained for scoring historical sessions from before complexity became
	// diagnostic-only. New executions should not produce these failed results.
	SimplificationSuggestions int `json:"simplification_suggestions"`
	FinalAnswerCharacters     int `json:"final_answer_characters"`
}

// ToolExecutionEvent must come from the local dispatcher or a controlled test
// dispatcher. Dispatched explicitly distinguishes execution failure from an
// approval denial, unavailable review, validation failure, or cancellation.
type ToolExecutionEvent struct {
	Err        error
	Dispatched bool
}

type reorganizationControl interface{ CommandReorganization() bool }

func ReorganizationKind(err error) (automatic, control bool) {
	var event reorganizationControl
	if !errors.As(err, &event) {
		return false, false
	}
	return event.CommandReorganization(), true
}

func MeasureExecutionWithEvents(messages []proto.Message, events []ToolExecutionEvent) ExecutionMetrics {
	m := MeasureExecution(messages)
	if events == nil {
		return m
	}
	automatic, manual, failures := 0, 0, 0
	for _, event := range events {
		if auto, control := ReorganizationKind(event.Err); control {
			if auto {
				automatic++
			} else {
				manual++
			}
		} else if event.Dispatched && event.Err != nil {
			failures++
		}
	}
	m.AutomaticReorganizations, m.ManualReorganizations, m.ExecutionFailures = &automatic, &manual, &failures
	return m
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
