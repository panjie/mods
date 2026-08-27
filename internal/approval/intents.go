package approval

import (
	"encoding/json"
	"strings"
)

// PromptIntent is a closed-enumeration label describing the capability the
// user's prompt requests. An LLM maps the prompt text onto these labels; the
// labels themselves carry no authorization — the approval gate interprets
// each label as a fixed capability (write workspace / read anywhere) and
// still enforces the access matrix's hard boundaries: external writes,
// unknown effects, and dynamic write targets always ask.
type PromptIntent string

const (
	// IntentWorkspaceEdit authorizes write operations whose target resolves
	// inside the workspace. A write with a statically unknown target is only
	// covered once a classifier confirms the workspace scope.
	IntentWorkspaceEdit PromptIntent = "workspace-edit"
	// IntentGlobalRead authorizes read operations anywhere, including paths
	// outside the workspace and runtime-resolved read targets.
	IntentGlobalRead PromptIntent = "global-read"
)

// ParsePromptIntent validates a classifier label against the closed set.
// Unknown labels are rejected so a hallucinating classifier cannot smuggle
// in new authorization categories.
func ParsePromptIntent(label string) (PromptIntent, bool) {
	switch PromptIntent(strings.TrimSpace(strings.ToLower(label))) {
	case IntentWorkspaceEdit:
		return IntentWorkspaceEdit, true
	case IntentGlobalRead:
		return IntentGlobalRead, true
	default:
		return "", false
	}
}

// ParsePromptIntentResponse decodes the classifier's JSON reply into valid
// intents. Malformed payloads and unknown labels are dropped; the empty
// result is the fail-closed default.
func ParsePromptIntentResponse(raw string) []PromptIntent {
	var parsed struct {
		Intents []string `json:"intents"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return nil
	}
	var intents []PromptIntent
	seen := map[string]bool{}
	for _, label := range parsed.Intents {
		intent, ok := ParsePromptIntent(label)
		if !ok || seen[string(intent)] {
			continue
		}
		seen[string(intent)] = true
		intents = append(intents, intent)
	}
	return intents
}
