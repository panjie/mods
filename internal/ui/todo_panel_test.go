package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func todoArgs() []byte {
	return []byte(`{"todos":[
		{"content":"measure startup time","status":"completed"},
		{"content":"analyze init.el","status":"in_progress"},
		{"content":"apply lazy-loading","status":"pending"}
	]}`)
}

func TestRenderTodoPanel(t *testing.T) {
	styles := MakeStyles(true).Interaction
	items := TodoItemsFromArgs(todoArgs())
	require.Len(t, items, 3)

	panel := RenderTodoPanel(styles, 80, items)
	stripped := ansi.Strip(panel)
	require.Contains(t, stripped, "PLAN")
	require.Contains(t, stripped, "1/3 completed")
	require.Contains(t, stripped, "1 in progress")
	require.Contains(t, stripped, "1. [x] measure startup time")
	require.Contains(t, stripped, "2. [~] analyze init.el")
	require.Contains(t, stripped, "3. [ ] apply lazy-loading")
}

func TestRenderTodoPanelHardwrapsLongContent(t *testing.T) {
	styles := MakeStyles(true).Interaction
	long := ""
	for i := 0; i < 40; i++ {
		long += "verylongstep "
	}
	panel := RenderTodoPanel(styles, 40, []TodoItem{{Content: long, Status: "pending"}})
	for _, line := range splitLines(panel) {
		require.LessOrEqual(t, ansi.StringWidth(line), 40+2)
	}
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	lines = append(lines, s[start:])
	return lines
}

func TestTodoItemsFromArgs(t *testing.T) {
	require.Nil(t, TodoItemsFromArgs(nil))
	require.Nil(t, TodoItemsFromArgs([]byte(`not json`)))
	require.Nil(t, TodoItemsFromArgs([]byte(`{"todos":[]}`)))
	require.Nil(t, TodoItemsFromArgs([]byte(`{}`)))
}

func TestTodoSummary(t *testing.T) {
	require.Equal(t, "3 items · 1 completed, 1 in progress", TodoSummary(todoArgs()))
	require.Equal(t, "2 items · 2 completed", TodoSummary([]byte(`{"todos":[
		{"content":"a","status":"completed"},
		{"content":"b","status":"completed"}
	]}`)))
	require.Empty(t, TodoSummary([]byte(`nope`)))
}

func TestTodoToolSurfaces(t *testing.T) {
	require.Equal(t, "Updating plan", ToolOperationLabel("todo_write", todoArgs(), 80))
	status := ToolResultStatus("todo_write", todoArgs(), nil, 80)
	require.Contains(t, status, "✓ todo_write: 3 items · 1 completed, 1 in progress")
}

func TestTodoFooterLine(t *testing.T) {
	styles := MakeStyles(true)

	t.Run("empty items render nothing", func(t *testing.T) {
		require.Empty(t, TodoFooterLine(styles, nil, 80))
	})

	t.Run("in-progress plan shows current step", func(t *testing.T) {
		line := TodoFooterLine(styles, TodoItemsFromArgs(todoArgs()), 80)
		stripped := ansi.Strip(line)
		require.Contains(t, stripped, "PLAN")
		require.Contains(t, stripped, "1/3")
		require.Contains(t, stripped, "▸ analyze init.el")
		require.True(t, strings.HasPrefix(stripped, "PLAN"), "badge leads the line: %q", stripped)
	})

	t.Run("no in-progress falls back to first pending", func(t *testing.T) {
		items := []TodoItem{
			{Content: "first", Status: "completed"},
			{Content: "second", Status: "pending"},
			{Content: "third", Status: "pending"},
		}
		stripped := ansi.Strip(TodoFooterLine(styles, items, 80))
		require.Contains(t, stripped, "1/3")
		require.Contains(t, stripped, "second")
		require.NotContains(t, stripped, "▸")
	})

	t.Run("all completed reports done", func(t *testing.T) {
		items := []TodoItem{
			{Content: "first", Status: "completed"},
			{Content: "second", Status: "completed"},
			{Content: "third", Status: "completed"},
		}
		stripped := ansi.Strip(TodoFooterLine(styles, items, 80))
		require.Contains(t, stripped, "3/3 done")
		require.NotContains(t, stripped, "▸")
	})

	t.Run("long step truncates to width", func(t *testing.T) {
		long := strings.Repeat("very long step ", 10)
		items := []TodoItem{{Content: long, Status: "in_progress"}}
		line := TodoFooterLine(styles, items, 40)
		require.LessOrEqual(t, ansi.StringWidth(ansi.Strip(line)), 40)
	})
}
