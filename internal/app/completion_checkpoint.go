package app

import (
	"encoding/json"
	"maps"
	"slices"

	"github.com/panjie/mods/internal/proto"
)

// A checkpoint is taken before the first request and after each completed tool
// batch. Retrying resumes the next model round, preserving tool results (including
// errors and denials) rather than asking the model to repeat side effects.
type completionCheckpoint struct {
	// Automatic organization budget deliberately lives on the user request,
	// outside this snapshot: restoring an older checkpoint cannot refund it.
	activityBlocks          map[string][]activityRecord
	messages                []proto.Message
	output                  string
	displayOutput           string
	displayBlocks           map[string]string
	toolResultBlocks        map[string]bool
	displayBlockSeq         int
	thought                 string
	thoughtFlushed          bool
	responseOutputStarted   bool
	responseBoundaryPending bool
}

func (m *Mods) setupCompletionContext(content string) error {
	if m.retries == 0 || m.retryCheckpoint == nil {
		return m.setupStreamContext(content)
	}
	m.messages = cloneCompletionMessages(m.retryCheckpoint.messages)
	// The registry can change when a retry selects a fallback provider. Rebuild
	// its guidance without duplicating these request-local system sections.
	m.toolSelectionInsertAt = -1
	m.messages = slices.DeleteFunc(m.messages, func(message proto.Message) bool {
		return message.Role == proto.RoleSystem &&
			(message.SystemSection() == proto.SystemSectionExecutionTools ||
				message.SystemSection() == proto.SystemSectionExecutionSelfHelp)
	})
	return nil
}

func (m *Mods) saveCompletionCheckpoint(messages []proto.Message) {
	m.retryCheckpoint = &completionCheckpoint{
		messages:                cloneCompletionMessages(messages),
		activityBlocks:          cloneActivityBlocks(m.activityBlocks),
		output:                  m.Output,
		displayOutput:           m.displayOutput,
		displayBlocks:           maps.Clone(m.displayBlocks),
		toolResultBlocks:        maps.Clone(m.toolResultBlocks),
		displayBlockSeq:         m.displayBlockSeq,
		thought:                 m.Thought,
		thoughtFlushed:          m.thoughtFlushed,
		responseOutputStarted:   m.responseOutputStarted,
		responseBoundaryPending: m.responseBoundaryPending,
	}
}

func (m *Mods) restoreCompletionOutput() {
	c := m.retryCheckpoint
	m.Output = c.output
	m.outputBuilder.WriteString(c.output)
	m.displayOutput = c.displayOutput
	m.displayOutputBuilder.WriteString(c.displayOutput)
	m.displayBlocks = maps.Clone(c.displayBlocks)
	m.activityBlocks = cloneActivityBlocks(c.activityBlocks)
	m.toolResultBlocks = maps.Clone(c.toolResultBlocks)
	m.displayBlockSeq = c.displayBlockSeq
	m.renderDirty = true
	m.Thought = c.thought
	m.thoughtFlushed = c.thoughtFlushed
	m.responseOutputStarted = c.responseOutputStarted
	m.responseBoundaryPending = c.responseBoundaryPending
}

func cloneCompletionMessages(messages []proto.Message) []proto.Message {
	cloned := slices.Clone(messages)
	for i := range cloned {
		message := &cloned[i]
		message.Media = slices.Clone(message.Media)
		for j := range message.Media {
			message.Media[j].Data = slices.Clone(message.Media[j].Data)
		}
		message.Images = slices.Clone(message.Images)
		for j := range message.Images {
			message.Images[j].Data = slices.Clone(message.Images[j].Data)
		}
		message.ToolCalls = slices.Clone(message.ToolCalls)
		for j := range message.ToolCalls {
			message.ToolCalls[j].Function.Arguments = slices.Clone(message.ToolCalls[j].Function.Arguments)
		}
		if message.ProviderData != nil {
			message.ProviderData = make(map[string]json.RawMessage, len(message.ProviderData))
			for key, value := range messages[i].ProviderData {
				message.ProviderData[key] = slices.Clone(value)
			}
		}
	}
	return cloned
}
