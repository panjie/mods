package cli

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestPromptAreaNoWrapKeepsLogicalLinesAndCursorVisible(t *testing.T) {
	m := newPromptArea(false)
	m.Prompt = ""
	m.ShowLineNumbers = false
	m.DynamicHeight = true
	m.MinHeight, m.MaxHeight = 1, 8
	m.SetVirtualCursor(false)
	m.Focus()
	m.SetWidth(18)
	content := strings.Repeat("界abc", 180)
	m.SetValue(content)
	m.MoveToEnd()
	require.Equal(t, 1, m.Height())
	require.Zero(t, m.LineInfo().RowOffset)
	require.Less(t, m.Cursor().Position.X, 18)
	for _, line := range strings.Split(m.View(), "\n") {
		require.LessOrEqual(t, ansi.StringWidth(line), 18)
	}
	m, _ = m.Update(tea.PasteMsg{Content: "\nsecond"})
	require.Equal(t, content+"\nsecond", m.Value())
	require.Equal(t, 2, m.Height())
	m.SetWidth(10)
	require.Equal(t, 2, m.Height())
}

func TestViewerDefaultsToNoWrap(t *testing.T) {
	oldWrap := config.WordWrap
	config.WordWrap = 0
	t.Cleanup(func() { config.WordWrap = oldWrap })
	m := newLoadedViewer(t, 20, 8, []proto.Message{{Role: proto.RoleUser, Content: strings.Repeat("x", 100)}})
	require.False(t, m.viewport.SoftWrap)
}
