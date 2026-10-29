// Package contextplan plans history compaction without modifying the transcript.
package contextplan

import "github.com/panjie/mods/internal/proto"

// Prefix returns the exclusive end of a compactable prefix, retaining at least
// keepTurns user turns. Zero means no safe prefix exists. System instructions
// must be assembled separately; they must never be summarized as conversation.
// Callers must retain the original transcript independently of the model view.
func Prefix(messages []proto.Message, keepTurns int) int {
	if keepTurns < 1 {
		return 0
	}
	var turns []int
	pending := map[string]bool{}
	valid := true
	for i, m := range messages {
		switch m.Role {
		case proto.RoleSystem:
			return 0
		case proto.RoleUser:
			// Adapters may project tool images into synthetic user messages.
			// Never treat those as a safe boundary inside a pending tool group.
			if len(pending) == 0 && valid {
				turns = append(turns, i)
			}
		case proto.RoleAssistant:
			for _, call := range m.ToolCalls {
				if call.ID == "" || pending[call.ID] {
					valid = false
				}
				pending[call.ID] = true
			}
		case proto.RoleTool:
			if len(m.ToolCalls) == 0 {
				valid = false
			}
			for _, call := range m.ToolCalls {
				if !pending[call.ID] {
					valid = false
				}
				delete(pending, call.ID)
			}
		}
	}
	// An in-flight or malformed transcript is never eligible for compaction.
	if !valid || len(pending) != 0 || len(turns) <= keepTurns {
		return 0
	}
	return turns[len(turns)-keepTurns]
}
