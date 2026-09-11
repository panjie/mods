package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

// Reserve room for the plan without padding the answer to a full screen.
// Short output grows naturally; overflowing output scrolls above the footer.
// One blank row separates the answer from the plan panel.
func (m *Mods) renderTodoLayout(content string) string {
	footer := m.footerView()
	gap := 0
	if strings.TrimSpace(content) != "" {
		gap = 1
	}
	height := max(1, m.height-lipgloss.Height(footer)-gap)
	m.setTodoViewport(content, m.width, height)
	if gap == 0 {
		return footer
	}
	return m.glamViewport.View() + "\n\n" + footer
}

func (m *Mods) setTodoViewport(content string, width, height int) {
	atBottom := m.glamViewport.AtBottom()
	content = ansi.Hardwrap(strings.TrimRight(content, "\n"), width, true)
	m.glamViewport.SetWidth(width)
	m.glamViewport.SetHeight(min(height, lipgloss.Height(content)))
	m.glamViewport.SetContent(content)
	if atBottom {
		m.glamViewport.GotoBottom()
	}
}

func (m *Mods) updateTodoPanel(data []byte) bool {
	items := ui.TodoItemsFromArgs(data)
	if len(items) == 0 {
		return false
	}
	m.todoItems = items
	return true
}

// Small terminals retain the compact footer instead of squeezing the answer.
func (m *Mods) todoPanelVisible() bool {
	if m.Config == nil || m.Config.Raw || m.Config.Minimal || m.Config.HideToolStatus ||
		!IsOutputTTY() || len(m.todoItems) == 0 || m.width < 40 || m.height < 10 {
		return false
	}
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
