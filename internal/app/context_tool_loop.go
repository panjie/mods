package app

import (
	"encoding/json"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/contextplan"
	"github.com/panjie/mods/internal/proto"
)

// compactToolLoopCmd runs only between committed tool batches and the next
// provider request. The worker owns copies; cancellation/save can still read
// the unchanged committed transcript while a summary request is in flight.
func (m *Mods) compactToolLoopCmd(runner *streamRunner) tea.Cmd {
	if m.Config.ContextMaxBytes <= 0 || runner.client == nil || runner.closed.Load() {
		return nil
	}
	view := conversationalMessagesForCompaction(m.messages)
	encoded, err := json.Marshal(view)
	if err != nil || len(encoded) <= m.Config.ContextMaxBytes {
		return nil
	}
	original := m.Messages()
	var system, history []proto.Message
	for _, message := range original {
		if message.Role == proto.RoleSystem {
			system = append(system, message)
		} else {
			history = append(history, message)
		}
	}
	cut := contextplan.ToolPrefix(history)
	if cut <= len(m.compactedPrefix) {
		return nil
	}
	// Keep the current user's literal input, including media, outside the summary.
	var current []proto.Message
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == proto.RoleUser {
			if i < cut {
				current = cloneCompletionMessages(history[i : i+1])
			}
			break
		}
	}
	worker := &Mods{ctx: runner.ctx, Config: m.Config,
		compactedPrefix: cloneCompletionMessages(m.compactedPrefix), compactedText: m.compactedText}
	m.setActiveOperation("Compacting conversation context…")
	return func() tea.Msg {
		err := worker.compactPrefix(runner.client, runner.model, system, history, cut)
		if err == nil && len(current) > 0 {
			at := len(system) + 1
			tail := cloneCompletionMessages(worker.messages[at:])
			worker.messages = append(worker.messages[:at], current...)
			worker.messages = append(worker.messages, tail...)
			worker.compactedRetained = len(current)
		}
		if err == nil {
			compacted, marshalErr := json.Marshal(conversationalMessagesForCompaction(worker.messages))
			if marshalErr != nil {
				err = marshalErr
			} else if len(compacted) >= len(encoded) {
				worker.messages = nil // Retaining the literal input can outweigh the summary's savings.
			}
		}
		return streamEventMsg{kind: streamEventCompacted, runner: runner, compaction: worker, err: err}
	}
}

func conversationalMessagesForCompaction(messages []proto.Message) []proto.Message {
	var history []proto.Message
	for _, message := range messages {
		if message.Role != proto.RoleSystem {
			history = append(history, message)
		}
	}
	return history
}

func (m *Mods) handleContextCompacted(msg streamEventMsg) tea.Cmd {
	m.setActiveOperation("")
	m.tokenUsage.Add(msg.compaction.tokenUsage)
	if msg.runner.closed.Load() {
		return nil
	}
	if msg.err == nil {
		msg.err = msg.runner.ctx.Err()
	}
	if msg.err != nil {
		msg.runner.close()
		m.tokenUsage.Add(msg.runner.takeUsage())
		return msgCmd(modsError{Err: msg.err, ReasonText: "Could not compact conversation context; completed tool results retained"})
	}
	worker := msg.compaction
	if worker.messages == nil {
		return msg.runner.receiveCmd()
	}
	m.compactedPrefix, m.compactedText = worker.compactedPrefix, worker.compactedText
	m.compactedRetained = worker.compactedRetained
	m.messages = worker.messages
	m.transcript.Replace(m.messages)
	if m.historyArchive != nil {
		m.historyArchive.Replace(m.compactedPrefix)
	}
	m.reportContextCompaction()
	debug.Printf("Compacted tool loop: archived=%d retained=%d", len(m.compactedPrefix), len(m.messages))
	return msg.runner.receiveCmd()
}
