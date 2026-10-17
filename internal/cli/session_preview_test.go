package cli

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestSessionPreviewIgnoresStaleSelectionAndFitsResize(t *testing.T) {
	m := newBrowserModel([]Session{{ID: "aaaaaaa", Title: "第一段会话", UpdatedAt: time.Now()}, {ID: "bbbbbbb", Title: "第二段会话", UpdatedAt: time.Now()}}, false)
	m.db = nil
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	firstRequest := m.previewRequest
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, "bbbbbbb", m.previewID)
	m.receivePreview(previewLoadedMsg{loadedContentMsg{id: "aaaaaaa", request: firstRequest, messages: []proto.Message{{Content: "stale"}}}})
	require.Empty(t, m.previewText)
	m.receivePreview(previewLoadedMsg{loadedContentMsg{id: "bbbbbbb", request: m.previewRequest, messages: []proto.Message{{Role: proto.RoleUser, Content: strings.Repeat("当前内容", 200)}}}})
	require.Contains(t, m.View().Content, "当前内容")
	require.NotContains(t, m.View().Content, "stale")
	for _, width := range []int{120, 110, 80, 30, 120} {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 24})
		view := m.View().Content
		for _, line := range strings.Split(view, "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, line)
		}
		require.LessOrEqual(t, lipgloss.Height(view), 24)
		if width < 110 {
			require.NotContains(t, view, "Enter opens full session")
		}
	}
	m.Update(tea.KeyPressMsg{Code: '?', Text: "?"})
	require.True(t, m.helpOpen)
	require.Contains(t, ansi.Strip(m.View().Content), "Keyboard shortcuts")
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	require.Equal(t, stateBrowsing, m.state, "help must not dispatch destructive shortcuts")
	m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.False(t, m.helpOpen)
}

func TestEmptyBrowserFitsWideScreen(t *testing.T) {
	m := newBrowserModel(nil, false)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	for _, line := range strings.Split(m.View().Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), 120)
	}
}

func TestDeleteRefreshesFocusedPreview(t *testing.T) {
	sessions := []Session{{ID: "aaaaaaa", Title: "first"}, {ID: "bbbbbbb", Title: "second"}}
	m := newBrowserModel(sessions, false)
	m.db = nil
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 24})
	m.previewLoaded = true
	m.previewText = "first content"
	m.Update(deletedMsg{fresh: sessions[1:], count: 1})
	require.Equal(t, "bbbbbbb", m.previewID)
	require.Empty(t, m.previewText)
	require.NotContains(t, m.View().Content, "first content")
}
