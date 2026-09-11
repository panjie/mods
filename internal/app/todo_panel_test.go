package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
	"github.com/stretchr/testify/require"
)

func todoWriteArgs() []byte {
	return []byte(`{"todos":[
		{"content":"measure startup time","status":"completed"},
		{"content":"analyze init.el","status":"in_progress"},
		{"content":"apply lazy-loading","status":"pending"}
	]}`)
}

func newTodoTestMods(t *testing.T) *Mods {
	t.Helper()
	gr, err := glamour.NewTermRenderer(glamour.WithStandardStyle("dark"))
	require.NoError(t, err)
	m := &Mods{
		Config:       &Config{},
		Styles:       makeStyles(true),
		state:        responseState,
		reviewer:     &toolReviewer{},
		contentMutex: &sync.Mutex{},
		width:        80,
	}
	m.glam = gr
	m.glamViewport = viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	return m
}

func withOutputTTY(t *testing.T, tty bool) {
	t.Helper()
	oldExported := IsOutputTTY
	oldUnexported := isOutputTTY
	IsOutputTTY = func() bool { return tty }
	isOutputTTY = func() bool { return tty }
	t.Cleanup(func() {
		IsOutputTTY = oldExported
		isOutputTTY = oldUnexported
	})
}

func TestTodoWriteUpdatesPanelWithoutAppendingOutput(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 120, 24
	m.showOperationStatus = true
	m.appendToOutput("Let me look at your config.")
	original := m.Output
	require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
	require.Equal(t, original, m.Output)
	require.Empty(t, m.displayBlocks)
	view := ansi.Strip(m.View().Content)
	require.Contains(t, view, "PLAN")
	require.Contains(t, view, "[~] analyze init.el")
	require.NotContains(t, m.glamOutput, "PLAN")
	require.Equal(t, 1, strings.Count(view, "PLAN"))
	require.Nil(t, m.toolResultOutputCmd("todo_write", completedTodoArgs(), nil))
	view = ansi.Strip(m.View().Content)
	require.Contains(t, view, "2/2 completed")
	require.NotContains(t, view, "analyze init.el")
	require.Equal(t, original, m.Output)
}

func TestTodoDockLayoutAndResize(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 120, 24
	m.showOperationStatus = true
	m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
	m.appendToOutput(strings.Repeat("Long answer with 中文 and more text. ", 160))
	view := m.View().Content
	require.LessOrEqual(t, lipgloss.Width(view), m.width)
	require.Equal(t, m.height, lipgloss.Height(view))
	require.Contains(t, ansi.Strip(view), "PLAN")
	require.Contains(t, m.footerView(), "PLAN")
	require.Equal(t, 120, m.glamViewport.Width())
	// The plan keeps its row when the answer scrolls.
	planRow := lineIndexContaining(strings.Split(view, "\n"), "PLAN")
	m.glamViewport.GotoTop()
	view = ansi.Strip(m.View().Content)
	require.Equal(t, planRow, lineIndexContaining(strings.Split(view, "\n"), "PLAN"))
	require.Greater(t, planRow, 0)
	_, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	view = ansi.Strip(m.View().Content)
	require.True(t, m.todoPanelVisible())
	require.Equal(t, 80, m.glamViewport.Width())
	require.Contains(t, view, "[~] analyze init.el")
	_, _ = m.Update(tea.WindowSizeMsg{Width: 32, Height: 8})
	view = ansi.Strip(m.View().Content)
	require.False(t, m.todoPanelVisible())
	require.Contains(t, view, "▸ analyze init.el")
	require.NotContains(t, view, "[~]")
	_, _ = m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	require.Contains(t, ansi.Strip(m.View().Content), "[~] analyze init.el")
}

func TestTodoWriteNonTTYWritesStderrSummary(t *testing.T) {
	withOutputTTY(t, false)
	m := &Mods{Config: &Config{}, contentMutex: &sync.Mutex{}}
	stderr := captureStderr(t, func() {
		require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
	})
	require.Contains(t, stderr, "✓ todo_write: 3 items · 1 completed, 1 in progress")
	require.Empty(t, m.Output)
	require.Nil(t, m.displayBlocks)
}

func TestTodoWriteSuppressedByModeFlags(t *testing.T) {
	withOutputTTY(t, true)
	newMods := func() *Mods {
		return &Mods{Config: &Config{}, contentMutex: &sync.Mutex{}, Styles: makeStyles(true)}
	}
	t.Run("raw", func(t *testing.T) {
		m := newMods()
		m.Config.Raw = true
		require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
		require.Empty(t, m.Output)
		require.Nil(t, m.displayBlocks)
	})
	t.Run("minimal", func(t *testing.T) {
		m := newMods()
		m.Config.Minimal = true
		require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
		require.Empty(t, m.Output)
		require.Nil(t, m.displayBlocks)
	})
	t.Run("hide-tool-status", func(t *testing.T) {
		m := newMods()
		m.Config.HideToolStatus = true
		require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
		require.Empty(t, m.Output)
		require.Nil(t, m.displayBlocks)
	})
}

func TestTodoWriteFailureAndBadArgsFallBackToStatusLine(t *testing.T) {
	withOutputTTY(t, true)

	t.Run("error writes failure line", func(t *testing.T) {
		m := &Mods{Config: &Config{}, contentMutex: &sync.Mutex{}}
		stderr := captureStderr(t, func() {
			require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), errors.New("boom")))
		})
		require.Contains(t, stderr, "✗ todo_write")
		require.Empty(t, m.Output)
	})

	t.Run("unparseable args write plain status line", func(t *testing.T) {
		m := &Mods{Config: &Config{}, contentMutex: &sync.Mutex{}}
		stderr := captureStderr(t, func() {
			require.Nil(t, m.toolResultOutputCmd("todo_write", []byte(`nope`), nil))
		})
		require.Contains(t, stderr, "✓ todo_write")
		require.Empty(t, m.Output)
	})
}

func TestTodoWriteUpdatesModelPlan(t *testing.T) {
	errh := func(error) tea.Msg { return nil }

	t.Run("successful call stores items", func(t *testing.T) {
		m := newAnimatingMods()
		cmd := m.handleToolCallsDone(streamEventMsg{
			results: []proto.ToolCallStatus{{Name: toolregistry.TodoWriteToolName, Arguments: todoWriteArgs()}},
			runner:  newStreamRunner(staticStream{}, nil, nil, errh),
		})
		require.NotNil(t, cmd)
		require.Len(t, m.todoItems, 3)
		require.Equal(t, "completed", m.todoItems[0].Status)
		require.Equal(t, "in_progress", m.todoItems[1].Status)
		require.Equal(t, "pending", m.todoItems[2].Status)
	})

	t.Run("failed call does not update", func(t *testing.T) {
		m := newAnimatingMods()
		m.todoItems = []ui.TodoItem{{Content: "old", Status: "pending"}}
		cmd := m.handleToolCallsDone(streamEventMsg{
			results: []proto.ToolCallStatus{{Name: toolregistry.TodoWriteToolName, Arguments: todoWriteArgs(), Err: errors.New("boom")}},
			runner:  newStreamRunner(staticStream{}, nil, nil, errh),
		})
		require.NotNil(t, cmd)
		require.Len(t, m.todoItems, 1)
		require.Equal(t, "old", m.todoItems[0].Content)
	})
}

func TestTodoFooterLineInFooter(t *testing.T) {
	withOutputTTY(t, true)

	t.Run("plan line renders above operation line", func(t *testing.T) {
		m := newAnimatingMods()
		m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
		m.setActiveOperation("Shell: go test ./...")
		footer := m.footerView()
		stripped := ansi.Strip(footer)
		require.Contains(t, stripped, "PLAN")
		require.Contains(t, stripped, "▸ analyze init.el")
		require.Contains(t, stripped, "RUNNING")
		require.Contains(t, stripped, "Shell: go test ./...")
		planIdx := strings.Index(stripped, "PLAN")
		opIdx := strings.Index(stripped, "RUNNING")
		require.Less(t, planIdx, opIdx, "plan line must render above the operation line")
	})

	t.Run("plan line renders without operation", func(t *testing.T) {
		m := newAnimatingMods()
		m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
		footer := m.footerView()
		require.Contains(t, ansi.Strip(footer), "PLAN")
	})

	t.Run("hide-tool-status suppresses plan line", func(t *testing.T) {
		m := newAnimatingMods()
		m.Config.HideToolStatus = true
		m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
		m.setActiveOperation("Shell: go test ./...")
		require.NotContains(t, ansi.Strip(m.footerView()), "PLAN")
	})

	t.Run("completed plan reports done", func(t *testing.T) {
		m := newAnimatingMods()
		m.todoItems = []ui.TodoItem{
			{Content: "a", Status: "completed"},
			{Content: "b", Status: "completed"},
		}
		require.Contains(t, ansi.Strip(m.footerView()), "2/2 done")
	})
}

func completedTodoArgs() []byte {
	return []byte(`{"todos":[
		{"content":"only step","status":"completed"},
		{"content":"second step","status":"completed"}
	]}`)
}

func TestSetupStreamContextTodoPlanLifecycle(t *testing.T) {
	newMods := func(sessionID string, db *DB) *Mods {
		cfg := defaultConfig()
		cfg.SessionReadFromID = sessionID
		return &Mods{Config: &cfg, Styles: makeStyles(true), ctx: context.Background(), db: db}
	}

	t.Run("completed plan cleared at turn start", func(t *testing.T) {
		m := newMods("", nil)
		m.todoItems = []ui.TodoItem{{Content: "a", Status: "completed"}, {Content: "b", Status: "completed"}}
		require.NoError(t, m.setupStreamContext("next question"))
		require.Nil(t, m.todoItems)
	})

	t.Run("in-progress plan kept at turn start", func(t *testing.T) {
		m := newMods("", nil)
		m.todoItems = []ui.TodoItem{{Content: "a", Status: "in_progress"}}
		require.NoError(t, m.setupStreamContext("next question"))
		require.Len(t, m.todoItems, 1)
	})

	t.Run("continue restores in-progress plan from history", func(t *testing.T) {
		db := testDB(t)
		id := NewID()
		require.NoError(t, db.SaveSession(id, "saved", "openai", "gpt-5", []proto.Message{
			{Role: proto.RoleUser, Content: "previous request"},
			{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{
				ID:       "call-1",
				Function: proto.Function{Name: toolregistry.TodoWriteToolName, Arguments: todoWriteArgs()},
			}}},
		}, nil))
		m := newMods(id, db)
		require.NoError(t, m.setupStreamContext("follow up"))
		require.Len(t, m.todoItems, 3)
		require.Equal(t, "in_progress", m.todoItems[1].Status)
	})

	t.Run("continue does not restore completed plan", func(t *testing.T) {
		db := testDB(t)
		id := NewID()
		require.NoError(t, db.SaveSession(id, "saved", "openai", "gpt-5", []proto.Message{
			{Role: proto.RoleUser, Content: "previous request"},
			{Role: proto.RoleAssistant, ToolCalls: []proto.ToolCall{{
				ID:       "call-1",
				Function: proto.Function{Name: toolregistry.TodoWriteToolName, Arguments: completedTodoArgs()},
			}}},
		}, nil))
		m := newMods(id, db)
		require.NoError(t, m.setupStreamContext("follow up"))
		require.Nil(t, m.todoItems)
	})

	t.Run("history without todo_write leaves plan unset", func(t *testing.T) {
		db := testDB(t)
		id := NewID()
		require.NoError(t, db.SaveSession(id, "saved", "openai", "gpt-5", []proto.Message{
			{Role: proto.RoleUser, Content: "previous request"},
			{Role: proto.RoleAssistant, Content: "previous answer"},
		}, nil))
		m := newMods(id, db)
		require.NoError(t, m.setupStreamContext("follow up"))
		require.Nil(t, m.todoItems)
	})
}

func TestTodoDockPreservesInputCursor(t *testing.T) {
	withOutputTTY(t, true)
	old := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = old })
	m := newTodoTestMods(t)
	m.width, m.height = 120, 24
	m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
	m.userInput = newUserInputManager(m.Config)
	m.userInput.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{Question: "Sign in", Kind: "form", Fields: []toolregistry.UserInputField{
			{Key: "username", Label: "Username", Kind: "text"},
		}}, resp: make(chan userInputResult, 1),
	}})
	m.appendToOutput(strings.Repeat("history\n\n", 50))
	view := m.View()
	require.NotNil(t, view.Cursor)
	require.Equal(t, 24, lipgloss.Height(view.Content))
	require.Equal(t, lineIndexContaining(strings.Split(view.Content, "\n"), "Username"), view.Cursor.Y)
	require.Contains(t, ansi.Strip(view.Content), "PLAN")
	require.True(t, strings.HasSuffix(m.footerView(), "\n\n"+m.statusFooterView()))
}

func TestTodoDockAboveStatus(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 80, 24
	m.showOperationStatus = true
	m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
	m.setActiveOperation("Shell: go test ./...")
	m.appendToOutput("Answer text")
	view := ansi.Strip(m.View().Content)
	require.Less(t, strings.Index(view, "Answer text"), strings.Index(view, "PLAN"))
	require.Less(t, strings.Index(view, "apply lazy-loading"), strings.Index(view, "RUNNING"))
	require.Less(t, lipgloss.Height(view), m.height)
}

func TestTodoDockGapAboveContent(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)
	m.width, m.height = 80, 40
	m.showOperationStatus = true
	m.appendToOutput("Answer text")
	require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
	lines := strings.Split(ansi.Strip(m.View().Content), "\n")
	contentRow := lineIndexContaining(lines, "Answer text")
	planRow := lineIndexContaining(lines, "PLAN")
	require.Equal(t, contentRow+2, planRow, "exactly one blank row must separate content from the plan")
	require.Empty(t, strings.TrimSpace(lines[contentRow+1]))
}

func TestTodoDockDoesNotPadShortOutput(t *testing.T) {
	withOutputTTY(t, true)
	for _, content := range []string{"", "Existing answer", "First line\nSecond line"} {
		t.Run(content, func(t *testing.T) {
			m := newTodoTestMods(t)
			m.width, m.height = 80, 40
			m.showOperationStatus = true
			m.appendToOutput(content)
			before := strings.TrimRight(m.glamOutput, "\n")
			require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
			view := m.View()
			require.False(t, view.AltScreen)
			if strings.TrimSpace(before) == "" {
				require.Equal(t, m.footerView(), view.Content)
			} else {
				require.Equal(t, lipgloss.Height(before)+1+lipgloss.Height(m.footerView()), lipgloss.Height(view.Content))
				normalize := func(s string) string {
					lines := strings.Split(ansi.Strip(s), "\n")
					for i := range lines {
						lines[i] = strings.TrimRight(lines[i], " ")
					}
					return strings.Join(lines, "\n")
				}
				require.True(t, strings.HasPrefix(normalize(view.Content), normalize(before)), "answer should remain before the plan")
			}
			require.False(t, m.viewportNeeded())
			for range 3 {
				require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))
				require.Equal(t, view.Content, m.View().Content)
			}
		})
	}
}

func TestTodoDockSeparatesApprovalPanel(t *testing.T) {
	withOutputTTY(t, true)
	for _, height := range []int{12, 24, 40} {
		m := newTodoTestMods(t)
		m.width, m.height = 80, height
		m.todoItems = ui.TodoItemsFromArgs(todoWriteArgs())
		m.reviewer = &toolReviewer{
			reviewMode: ReviewAuto, reviewPending: true,
			reviewItem: &toolReviewItem{
				name: "shell_run", args: []byte(`{"command":"touch example.txt"}`),
				summary: "Run touch example.txt", resp: make(chan reviewResponse, 1),
			},
		}
		footer := m.footerView()
		approval := m.statusFooterView()
		require.Contains(t, ansi.Strip(footer), "PLAN")
		require.True(t, strings.HasSuffix(footer, "\n\n"+approval))
		require.LessOrEqual(t, lipgloss.Height(m.View().Content), height)
		m.todoItems = nil
		require.Equal(t, approval, m.footerView(), "no leading blank row without a plan")
	}
}
