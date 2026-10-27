package app

import "encoding/json"

// commandReorganizationError reports a control decision, not a process failure.
// The original tool call has not executed and must never be replayed implicitly.
type commandReorganizationError struct {
	ReasonCodes []string
}

func (e *commandReorganizationError) Error() string {
	payload := struct {
		Type        string   `json:"type"`
		Version     int      `json:"version"`
		Status      string   `json:"status"`
		ReasonCodes []string `json:"reason_codes"`
		Guidance    string   `json:"guidance"`
	}{
		Type:        "mods.command_reorganization",
		Version:     1,
		Status:      "not_executed",
		ReasonCodes: e.ReasonCodes,
		Guidance:    "Preserve the requested scope. Prefer one collection and a clear aggregation pipeline. Do not split by file or hide the same code in a script. The original call was not executed; any replacement call requires fresh analysis and approval.",
	}
	data, _ := json.Marshal(payload)
	return string(data)
}
