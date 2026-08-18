package app

import (
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

func (m *Mods) appendTodoPanel(data []byte) bool {
	items := ui.TodoItemsFromArgs(data)
	if len(items) == 0 {
		return false
	}
	m.appendToOutputWithDisplayBlock(ui.TodoPlainText(items), ui.RenderTodoPanel(m.Styles.Interaction, m.width, items))
	return true
}

// todoItemsAllCompleted reports whether a plan exists and every step is
// completed. Empty plans are not "completed" so they never trigger the
// turn-start reset by themselves.
func todoItemsAllCompleted(items []ui.TodoItem) bool {
	if len(items) == 0 {
		return false
	}
	for _, item := range items {
		if item.Status != "completed" {
			return false
		}
	}
	return true
}

// todoItemsFromMessages returns the most recent todo_write plan embedded in
// the message history, or nil when there is none or it is fully completed —
// completed plans are not restored across sessions, while in-progress plans
// survive --continue so follow-up turns keep their footer progress.
func todoItemsFromMessages(messages []proto.Message) []ui.TodoItem {
	for i := len(messages) - 1; i >= 0; i-- {
		for j := len(messages[i].ToolCalls) - 1; j >= 0; j-- {
			call := messages[i].ToolCalls[j]
			if call.Function.Name != toolregistry.TodoWriteToolName {
				continue
			}
			items := ui.TodoItemsFromArgs(call.Function.Arguments)
			if len(items) == 0 || todoItemsAllCompleted(items) {
				return nil
			}
			return items
		}
	}
	return nil
}
