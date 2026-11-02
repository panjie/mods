package app

import (
	"maps"
	"slices"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

// A checkpoint captures presentation state before the first request and after
// each completed tool batch. Retry history comes from the committed Transcript;
// the checkpoint only rolls back output from an incomplete model response.
type completionCheckpoint struct {
	activityBlocks          map[string][]activityRecord
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
	m.messages = m.transcript.Snapshot()
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
	if m.transcript == nil {
		m.transcript = &stream.Transcript{}
	}
	m.transcript.Replace(messages)
	m.retryCheckpoint = &completionCheckpoint{
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
	return proto.CloneMessages(messages)
}
