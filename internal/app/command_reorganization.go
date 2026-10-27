package app

import (
	"encoding/json"
	"slices"
	"strings"
)

// commandReorganizationError reports a control decision, not a process failure.
// The original tool call has not executed and must never be replayed implicitly.
type commandReorganizationError struct {
	ReasonCodes []string
	Automatic   bool
}

// CommandReorganization identifies trusted local control events to evaluators;
// arbitrary tool output containing the JSON text cannot implement this method.
func (e *commandReorganizationError) CommandReorganization() bool { return e.Automatic }

func (e *commandReorganizationError) Error() string {
	guidance := "Preserve the requested scope. Prefer one collection and a clear aggregation pipeline. Do not split by file or hide the same code in a script. The original call was not executed; any replacement call requires fresh analysis and approval."
	if e.Automatic {
		guidance = "Reorganize this command so its operations and results are clear while preserving the user's full scope, data requirements, and error handling. Prefer supported direct commands and a clear pipeline; do not split by file or move the same code into a script. The original call was not executed. Any replacement call requires fresh analysis and ordinary approval."
		var details []string
		if slices.Contains(e.ReasonCodes, "nested_control_flow") {
			details = append(details, "Nested control flow has unproven effects.")
		}
		if slices.Contains(e.ReasonCodes, "stateful_aggregation") {
			details = append(details, "Control flow shares multiple mutable local variables.")
		}
		if slices.Contains(e.ReasonCodes, "dynamic_evaluation") {
			details = append(details, "Dynamic evaluation hides the operations being run.")
		}
		if slices.Contains(e.ReasonCodes, "nested_inline_hosts") {
			details = append(details, "Nested interpreter payloads hide the execution structure.")
		}
		guidance = strings.Join(details, " ") + " " + guidance
	}
	var remaining *int
	if e.Automatic {
		zero := 0
		remaining = &zero
	}
	payload := struct {
		Type               string   `json:"type"`
		Version            int      `json:"version"`
		Status             string   `json:"status"`
		ReasonCodes        []string `json:"reason_codes"`
		Guidance           string   `json:"guidance"`
		AutomaticRemaining *int     `json:"automatic_remaining,omitempty"`
	}{
		Type:               "mods.command_reorganization",
		Version:            1,
		Status:             "not_executed",
		ReasonCodes:        e.ReasonCodes,
		Guidance:           guidance,
		AutomaticRemaining: remaining,
	}
	data, _ := json.Marshal(payload)
	return string(data)
}
