package cli

import (
	"errors"
	"image/color"
	"strings"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func newLoadedViewer(t *testing.T, width, height int, messages []proto.Message) *browserModel {
	t.Helper()
	m := newBrowserModel(nil)
	m.width = width
	m.height = height
	m.state = stateViewing
	m.viewerHeader = "session\nmetadata\nrule"
	m.viewerSearch = newViewerSearch(width)
	m.resetViewerViewport(height - lipgloss.Height(m.viewerHeader) - browserFooterHeight)
	_, cmd := m.handleLoaded(loadedContentMsg{messages: messages})
	require.Nil(t, cmd)
	return m
}

func sendViewerKey(m *browserModel, code rune, text string) {
	_, _ = m.Update(tea.KeyPressMsg{Code: code, Text: text})
}

func TestBuildTranscriptDocumentIsPlainRoleAwareContent(t *testing.T) {
	doc := buildTranscriptDocument([]proto.Message{
		{Role: proto.RoleUser, Content: "\x1b[31mfirst\x1b[0m\a\r\nsecond"},
		{Role: proto.RoleTool, Content: "secret tool result"},
		{Role: proto.RoleAssistant, Content: "answer"},
		{Role: proto.RoleSystem, Content: ""},
	})

	require.Equal(t, "first\nsecond\n\nanswer", doc.content)
	require.Equal(t, doc.content, ansi.Strip(doc.content))
	require.Len(t, doc.lines, 4)
	require.Equal(t, transcriptLine{role: proto.RoleUser, start: true}, doc.lines[0])
	require.Equal(t, transcriptLine{role: proto.RoleUser}, doc.lines[1])
	require.Equal(t, transcriptLine{}, doc.lines[2])
	require.Equal(t, transcriptLine{role: proto.RoleAssistant, start: true}, doc.lines[3])
	require.NotContains(t, doc.content, "tool")
}

func TestViewerGutterUsesFixedWidthRoleMarkers(t *testing.T) {
	m := newLoadedViewer(t, 40, 12, []proto.Message{
		{Role: proto.RoleUser, Content: "first\nsecond"},
		{Role: proto.RoleAssistant, Content: "answer"},
	})

	tests := []struct {
		ctx  viewport.GutterContext
		want string
	}{
		{viewport.GutterContext{Index: 0}, "U │ "},
		{viewport.GutterContext{Index: 0, Soft: true}, "  │ "},
		{viewport.GutterContext{Index: 1}, "  │ "},
		{viewport.GutterContext{Index: 2}, "    "},
		{viewport.GutterContext{Index: 3}, "A │ "},
	}
	for _, tt := range tests {
		got := m.viewerGutter(tt.ctx)
		require.Equal(t, transcriptGutterWidth, lipgloss.Width(got))
		require.Equal(t, tt.want, ansi.Strip(got))
	}

	dark := m.viewerGutter(viewport.GutterContext{Index: 0})
	_, _ = m.Update(tea.BackgroundColorMsg{Color: color.White})
	light := m.viewerGutter(viewport.GutterContext{Index: 0})
	require.NotEqual(t, dark, light)
}

func TestSmartLiteralMatches(t *testing.T) {
	t.Run("lowercase ignores case", func(t *testing.T) {
		matches, truncated := smartLiteralMatches("Error error ERROR", "error", 10)
		require.Len(t, matches, 3)
		require.False(t, truncated)
	})
	t.Run("uppercase enables case sensitivity", func(t *testing.T) {
		matches, _ := smartLiteralMatches("Error error ERROR", "Error", 10)
		require.Equal(t, [][]int{{0, 5}}, matches)
	})
	t.Run("regexp syntax is literal", func(t *testing.T) {
		matches, _ := smartLiteralMatches("a.b axb a.b", "a.b", 10)
		require.Equal(t, [][]int{{0, 3}, {8, 11}}, matches)
	})
	t.Run("unicode and explicit newline", func(t *testing.T) {
		matches, _ := smartLiteralMatches("中文\n搜索 中文", "中文\n搜索", 10)
		require.Equal(t, [][]int{{0, len("中文\n搜索")}}, matches)
	})
	t.Run("caps highlights", func(t *testing.T) {
		matches, truncated := smartLiteralMatches(strings.Repeat("a", maxViewerHighlights+2), "a", maxViewerHighlights)
		require.Len(t, matches, maxViewerHighlights)
		require.True(t, truncated)
	})
}

func TestViewerSearchInputAndNavigation(t *testing.T) {
	m := newLoadedViewer(t, 60, 12, []proto.Message{{
		Role:    proto.RoleUser,
		Content: "foo 中文 foo",
	}})

	sendViewerKey(m, '/', "/")
	require.True(t, m.viewerSearch.active)
	view := m.View()
	require.NotNil(t, view.Cursor)
	require.Equal(t, m.height-browserFooterHeight, view.Cursor.Y)
	before := view.Cursor.Position
	sendViewerKey(m, '中', "中")
	after := m.View()
	require.NotNil(t, after.Cursor)
	require.NotEqual(t, before, after.Cursor.Position)
	m.viewerSearch.input.SetValue("")

	for _, r := range "foo" {
		sendViewerKey(m, r, string(r))
	}
	sendViewerKey(m, tea.KeyEnter, "")
	require.False(t, m.viewerSearch.active)
	require.Equal(t, "foo", m.viewerSearch.query)
	require.Len(t, m.viewerSearch.matches, 2)
	require.Equal(t, 0, m.viewerSearch.index)
	require.Nil(t, m.View().Cursor)
	require.Contains(t, m.viewFooter(), "1/2")
	rendered := m.viewport.View()
	require.Contains(t, rendered, m.styles.selectedHighlight.Render("foo"))
	require.Contains(t, rendered, m.styles.highlight.Render("foo"))

	sendViewerKey(m, 'n', "n")
	require.Equal(t, 1, m.viewerSearch.index)
	sendViewerKey(m, 'n', "n")
	require.Equal(t, 0, m.viewerSearch.index)
	sendViewerKey(m, 'N', "N")
	require.Equal(t, 1, m.viewerSearch.index)

	previousMatches := m.viewerSearch.matches
	sendViewerKey(m, '/', "/")
	sendViewerKey(m, 'x', "x")
	sendViewerKey(m, tea.KeyEsc, "")
	require.False(t, m.viewerSearch.active)
	require.Equal(t, "foo", m.viewerSearch.query)
	require.Equal(t, previousMatches, m.viewerSearch.matches)

	sendViewerKey(m, '/', "/")
	sendViewerKey(m, tea.KeyEnter, "")
	require.Empty(t, m.viewerSearch.query)
	require.Empty(t, m.viewerSearch.matches)
	require.NotContains(t, m.viewFooter(), "1/2")
}

func TestViewerSearchNoMatchesPreservesPositionAndReportsStatus(t *testing.T) {
	m := newLoadedViewer(t, 14, 8, []proto.Message{{
		Role:    proto.RoleAssistant,
		Content: strings.Repeat("x", 200),
	}})
	m.viewport.SetYOffset(5)
	m.applyViewerSearch("absent")
	require.Equal(t, 5, m.viewport.YOffset())
	require.Empty(t, m.viewerSearch.matches)
	require.Contains(t, m.viewFooter(), "no matches")

	m.applyViewerSearch("x")
	require.Len(t, m.viewerSearch.matches, maxViewerHighlights/5)
	require.False(t, m.viewerSearch.truncated)
}

func TestViewerSearchCorrectsLongSoftWrappedMatchPosition(t *testing.T) {
	content := strings.Repeat("x", 75) + "needle" + strings.Repeat("x", 39) + "needle" + strings.Repeat("x", 100)
	m := newLoadedViewer(t, 14, 8, []proto.Message{{Role: proto.RoleAssistant, Content: content}})
	require.True(t, m.viewport.SoftWrap)
	require.Equal(t, 10, m.viewerLayout.width)

	m.applyViewerSearch("needle")
	require.Equal(t, 7, m.viewport.YOffset())
	sendViewerKey(m, 'n', "n")
	require.Equal(t, 12, m.viewport.YOffset())
	sendViewerKey(m, 'N', "N")
	require.Equal(t, 7, m.viewport.YOffset())

	_, _ = m.Update(tea.WindowSizeMsg{Width: 9, Height: 8})
	require.Equal(t, 5, m.viewerLayout.width)
	require.Equal(t, 15, m.viewport.YOffset())
	require.Equal(t, 0, m.viewerSearch.index)
}

func TestViewerResizeAndWrapTogglePreserveReadingAnchor(t *testing.T) {
	m := newLoadedViewer(t, 24, 8, []proto.Message{{
		Role:    proto.RoleUser,
		Content: strings.Repeat("界", 150),
	}})
	require.Equal(t, 20, m.viewerLayout.width)
	m.viewport.SetYOffset(5)

	_, _ = m.Update(tea.WindowSizeMsg{Width: 14, Height: 8})
	require.Equal(t, 10, m.viewerLayout.width)
	require.Equal(t, 10, m.viewport.YOffset())

	sendViewerKey(m, 'w', "w")
	require.False(t, m.viewport.SoftWrap)
	require.Equal(t, 0, m.viewport.YOffset())
	require.Equal(t, 100, m.viewport.XOffset())

	sendViewerKey(m, 'w', "w")
	require.True(t, m.viewport.SoftWrap)
	require.Equal(t, 10, m.viewport.YOffset())
}

func TestViewerNoWrapHorizontalKeysAndShiftWheel(t *testing.T) {
	m := newLoadedViewer(t, 20, 8, []proto.Message{{
		Role:    proto.RoleUser,
		Content: strings.Repeat("x", 100),
	}})
	sendViewerKey(m, 'w', "w")
	require.False(t, m.viewport.SoftWrap)
	m.viewport.SetXOffset(0)

	sendViewerKey(m, 'l', "l")
	require.Equal(t, viewerHorizontalStep, m.viewport.XOffset())
	sendViewerKey(m, 'h', "h")
	require.Zero(t, m.viewport.XOffset())

	_, _ = m.Update(tea.MouseWheelMsg(tea.Mouse{
		Button: tea.MouseWheelDown,
		Mod:    tea.ModShift,
	}))
	require.Equal(t, viewerHorizontalStep, m.viewport.XOffset())
	require.Equal(t, tea.MouseModeCellMotion, m.View().MouseMode)
}

func TestViewerHighlightLimitSummary(t *testing.T) {
	m := newLoadedViewer(t, 40, 10, []proto.Message{{
		Role:    proto.RoleUser,
		Content: strings.Repeat("a", maxViewerHighlights+2),
	}})
	m.applyViewerSearch("a")
	require.True(t, m.viewerSearch.truncated)
	require.Contains(t, m.viewFooter(), "1/1000+")
	require.Contains(t, m.viewFooter(), "refine search")
	require.LessOrEqual(t, lipgloss.Width(m.viewFooter()), m.width)
}

func TestViewerLoadingErrorEmptyAndCloseReset(t *testing.T) {
	m := newBrowserModel(nil)
	m.width, m.height = 40, 10
	m.state = stateViewing
	m.viewerHeader = "session\nmetadata\nrule"
	m.viewerSearch = newViewerSearch(m.width)
	m.resetViewerViewport(6)

	sendViewerKey(m, '/', "/")
	require.False(t, m.viewerSearch.active)
	require.Contains(t, ansi.Strip(m.viewViewer()), "Loading")

	_, _ = m.handleLoaded(loadedContentMsg{err: errors.New("load failed")})
	require.Contains(t, ansi.Strip(m.viewViewer()), "couldn't load session")

	_, _ = m.handleLoaded(loadedContentMsg{})
	require.Contains(t, ansi.Strip(m.viewViewer()), "no messages")

	m.applyViewerSearch("query")
	m.closeViewer()
	require.Equal(t, stateBrowsing, m.state)
	require.Empty(t, m.viewerDoc.content)
	require.Empty(t, m.viewerSearch.query)
}
