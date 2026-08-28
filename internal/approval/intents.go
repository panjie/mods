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

// WriteScope is a closed-enumeration label describing where a write
// command's local filesystem effect lands. Remote/network effects are
// deliberately absent: the approval matrix only guards local directories, so
// a command with no local write (purely remote) is out of scope.
type WriteScope string

const (
	// WriteScopeWorkspace means the command writes local files only within
	// the workspace (including .git, node_modules, build artifacts, and
	// cwd-relative outputs).
	WriteScopeWorkspace WriteScope = "workspace"
	// WriteScopeExternal means the command writes local files outside the
	// workspace (system config, global config, absolute paths elsewhere).
	WriteScopeExternal WriteScope = "external"
	// WriteScopeUnknown means the local write target cannot be determined.
	WriteScopeUnknown WriteScope = "unknown"
)

// ParseWriteScope validates a write-scope classifier label.
func ParseWriteScope(label string) (WriteScope, bool) {
	switch WriteScope(strings.TrimSpace(strings.ToLower(label))) {
	case WriteScopeWorkspace:
		return WriteScopeWorkspace, true
	case WriteScopeExternal:
		return WriteScopeExternal, true
	case WriteScopeUnknown:
		return WriteScopeUnknown, true
	default:
		return "", false
	}
}

// ParseWriteScopeResponse decodes the classifier's JSON reply into write
// scopes. Unlike prompt-intent parsing, any unrecognized label fails the
// whole response (nil): dropping a hallucinated "blocking" scope (for
// example a mistyped "external") could otherwise downgrade an ask into an
// allow. A missing "scopes" field also fails closed; an explicit empty array
// is valid and means "no local filesystem write" (a purely remote/network
// operation).
func ParseWriteScopeResponse(raw string) []WriteScope {
	var parsed struct {
		Scopes *[]string `json:"scopes"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return nil
	}
	if parsed.Scopes == nil {
		return nil
	}
	var scopes []WriteScope = []WriteScope{}
	seen := map[string]bool{}
	for _, label := range *parsed.Scopes {
		scope, ok := ParseWriteScope(label)
		if !ok || seen[string(scope)] {
			return nil
		}
		seen[string(scope)] = true
		scopes = append(scopes, scope)
	}
	return scopes
}
