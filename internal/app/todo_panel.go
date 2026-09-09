package app

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

// Keep the footer full-width so review banners and input cursors retain their
// normal coordinates. Only the answer viewport shares its rows with the plan.
func (m *Mods) renderTodoLayout(content string) string {
	footer := m.footerView()
	height := max(1, m.height-lipgloss.Height(footer))
	if footer == "" {
		height = m.height
	}
	width := m.todoSidebarWidth()
	m.setTodoViewport(content, m.width-width-2, height)
	left := lipgloss.NewStyle().Width(m.width - width - 2).Height(height).Render(m.glamViewport.View())
	right := ui.RenderTodoSidebar(m.Styles.Interaction, width, height, m.todoItems)
	body := lipgloss.JoinHorizontal(lipgloss.Top, left, "  ", right)
	if footer != "" {
		body += "\n" + footer
	}
	return body
}

func (m *Mods) setTodoViewport(content string, width, height int) {
	atBottom := m.glamViewport.AtBottom()
	m.glamViewport.SetWidth(width)
	m.glamViewport.SetHeight(height)
	m.glamViewport.SetContent(ansi.Hardwrap(strings.TrimRight(content, "\n"), width, true))
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
func (m *Mods) todoSidebarWidth() int {
	if m.Config == nil || m.Config.Raw || m.Config.Minimal || m.Config.HideToolStatus ||
		!IsOutputTTY() || len(m.todoItems) == 0 || m.width < 100 || m.height < 10 {
		return 0
	}
	return min(40, m.width/3)
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
