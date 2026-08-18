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

func TestTodoWriteRendersInlinePanel(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)

	require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))

	require.Contains(t, m.Output, "Plan (3 items)")
	require.Contains(t, m.Output, "1. [x] measure startup time")
	require.Contains(t, m.Output, "2. [~] analyze init.el")
	require.Contains(t, m.Output, "3. [ ] apply lazy-loading")

	require.Contains(t, m.displayOutput, "MODS_DISPLAY_BLOCK_1")
	plain := ansi.Strip(m.glamOutput)
	require.Contains(t, plain, "PLAN")
	require.Contains(t, plain, "1/3 completed")
	require.Contains(t, plain, "[x] measure startup time")
	require.Contains(t, plain, "[~] analyze init.el")
	require.NotContains(t, plain, "MODS_DISPLAY_BLOCK_1")
}

func TestTodoPanelAfterUnfinishedStreamText(t *testing.T) {
	withOutputTTY(t, true)
	m := newTodoTestMods(t)

	// A todo_write can land mid-stream while the model's preceding text has
	// no trailing newline. The display-block marker must still start on its
	// own line so glamour word wrap cannot glue it to the text and defeat
	// the exact-line replacement in replaceDisplayBlocks.
	m.appendToOutput("I'd be happy to help. Let me look at your config.")
	require.Nil(t, m.toolResultOutputCmd("todo_write", todoWriteArgs(), nil))

	plain := ansi.Strip(m.glamOutput)
	require.Contains(t, plain, "PLAN")
	require.Contains(t, plain, "[~] analyze init.el")
	require.NotContains(t, plain, "MODS_DISPLAY_BLOCK_1")

	require.Contains(t, m.Output, "your config.\n\nPlan (3 items)")
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
