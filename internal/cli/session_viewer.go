package cli

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
)

const (
	transcriptGutterWidth = 4
	viewerHorizontalStep  = 6
	maxViewerHighlights   = 1000
)

type transcriptLine struct {
	role  string
	start bool
}

type transcriptDocument struct {
	content string
	lines   []transcriptLine
}

// buildTranscriptDocument keeps the viewport content free of terminal
// styling so viewport highlight byte ranges always address the same string.
// Role styling is rendered separately by the viewport gutter.
func buildTranscriptDocument(messages []proto.Message) transcriptDocument {
	var contentLines []string
	var lineInfo []transcriptLine
	for _, msg := range messages {
		content := proto.TranscriptContent(msg)
		if content == "" {
			continue
		}
		if len(contentLines) > 0 {
			contentLines = append(contentLines, "")
			lineInfo = append(lineInfo, transcriptLine{})
		}

		content = plainTranscriptContent(content)
		parts := strings.Split(content, "\n")
		for i, line := range parts {
			contentLines = append(contentLines, line)
			lineInfo = append(lineInfo, transcriptLine{role: msg.Role, start: i == 0})
		}
	}
	return transcriptDocument{content: strings.Join(contentLines, "\n"), lines: lineInfo}
}

func plainTranscriptContent(content string) string {
	content = ansi.Strip(content)
	content = strings.ReplaceAll(content, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	return strings.Map(func(r rune) rune {
		if r != '\n' && r != '\t' && unicode.IsControl(r) {
			return -1
		}
		return r
	}, content)
}

type transcriptLayout struct {
	width      int
	soft       bool
	starts     []int
	heights    []int
	byteStarts []int
	total      int
}

func newTranscriptLayout(content string, width int, soft bool) transcriptLayout {
	layout := transcriptLayout{width: max(1, width), soft: soft}
	if content == "" {
		return layout
	}
	lines := strings.Split(content, "\n")
	layout.starts = make([]int, len(lines))
	layout.heights = make([]int, len(lines))
	layout.byteStarts = make([]int, len(lines))
	byteStart := 0
	for i, line := range lines {
		layout.starts[i] = layout.total
		layout.byteStarts[i] = byteStart
		height := 1
		if soft {
			height = max(1, (ansi.StringWidth(line)+layout.width-1)/layout.width)
		}
		layout.heights[i] = height
		layout.total += height
		byteStart += len(line) + 1
	}
	return layout
}

type viewerAnchor struct {
	line int
	cell int
}

func (l transcriptLayout) anchorAt(y int) viewerAnchor {
	if len(l.starts) == 0 {
		return viewerAnchor{}
	}
	line := sort.Search(len(l.starts), func(i int) bool { return l.starts[i] > y }) - 1
	line = max(0, min(line, len(l.starts)-1))
	cell := 0
	if l.soft {
		cell = max(0, y-l.starts[line]) * l.width
	}
	return viewerAnchor{line: line, cell: cell}
}

func (l transcriptLayout) yFor(anchor viewerAnchor) int {
	if len(l.starts) == 0 {
		return 0
	}
	line := max(0, min(anchor.line, len(l.starts)-1))
	if !l.soft {
		return line
	}
	segment := anchor.cell / l.width
	segment = min(segment, max(0, l.heights[line]-1))
	return l.starts[line] + segment
}

func (l transcriptLayout) matchPosition(content string, byteOffset int) (line, cell, virtualY int) {
	if len(l.byteStarts) == 0 {
		return 0, 0, 0
	}
	line = sort.Search(len(l.byteStarts), func(i int) bool { return l.byteStarts[i] > byteOffset }) - 1
	line = max(0, min(line, len(l.byteStarts)-1))
	start := l.byteStarts[line]
	byteOffset = max(start, min(byteOffset, len(content)))
	cell = ansi.StringWidth(content[start:byteOffset])
	virtualY = l.starts[line]
	if l.soft {
		virtualY += cell / l.width
	}
	return line, cell, virtualY
}

type viewerSearch struct {
	input     textinput.Model
	active    bool
	query     string
	matches   [][]int
	index     int
	truncated bool
}

func newViewerSearch(width int) viewerSearch {
	input := textinput.New()
	input.Prompt = "/ "
	input.Placeholder = "find in session"
	input.SetVirtualCursor(false)
	input.SetWidth(max(1, width))
	return viewerSearch{input: input, index: -1}
}

func (s *viewerSearch) resize(width int) {
	if s.input.Width() != 0 || width > 0 {
		s.input.SetWidth(max(1, width))
	}
}

func (s *viewerSearch) view(width int) string {
	s.resize(width)
	view := s.input.View()
	if gap := width - lipgloss.Width(view); gap > 0 {
		view += strings.Repeat(" ", gap)
	}
	return view
}

func (s viewerSearch) summary(fallback string) string {
	if s.query == "" {
		return fallback
	}
	query := truncateRune(cleanSearchLabel(s.query), 18)
	if len(s.matches) == 0 {
		return "no matches “" + query + "”"
	}
	total := ""
	if s.truncated {
		total = "1000+"
	} else {
		total = stringInt(len(s.matches))
	}
	result := stringInt(s.index+1) + "/" + total + " “" + query + "”"
	if s.truncated {
		result += " refine search"
	}
	return result
}

func cleanSearchLabel(s string) string {
	s = ansi.Strip(s)
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}

func stringInt(n int) string {
	// fmt.Sprintf is relatively expensive in View; strconv keeps this helper
	// small while avoiding repeated formatting boilerplate at call sites.
	return strconv.Itoa(n)
}

func (m *browserModel) resetViewerViewport(height int) {
	m.viewport = viewport.New(
		viewport.WithWidth(m.width),
		viewport.WithHeight(max(1, height)),
	)
	m.viewport.SoftWrap = true
	m.viewport.SetHorizontalStep(viewerHorizontalStep)
	m.applyViewerStyles()
}

func (m *browserModel) applyViewerStyles() {
	m.viewport.LeftGutterFunc = m.viewerGutter
	m.viewport.HighlightStyle = m.styles.highlight
	m.viewport.SelectedHighlightStyle = m.styles.selectedHighlight
}

func (m *browserModel) viewerGutter(ctx viewport.GutterContext) string {
	if ctx.Index < 0 || ctx.Index >= len(m.viewerDoc.lines) {
		return strings.Repeat(" ", transcriptGutterWidth)
	}
	line := m.viewerDoc.lines[ctx.Index]
	if line.role == "" {
		return strings.Repeat(" ", transcriptGutterWidth)
	}

	style := m.styles.roleSystem
	marker := "S"
	switch line.role {
	case proto.RoleUser:
		style, marker = m.styles.roleUser, "U"
	case proto.RoleAssistant:
		style, marker = m.styles.roleAssistant, "A"
	case proto.RoleTool:
		style, marker = m.styles.roleSystem, "T"
	case proto.RoleSystem:
		style, marker = m.styles.roleSystem, "S"
	default:
		marker = "·"
	}
	if ctx.Soft || !line.start {
		marker = " "
	}
	return style.Render(marker) + " " + style.Render("│") + " "
}

func (m *browserModel) viewerContentWidth() int {
	return max(1, m.viewport.Width()-transcriptGutterWidth)
}

func (m *browserModel) rebuildViewerLayout() {
	m.viewerLayout = newTranscriptLayout(
		m.viewerDoc.content,
		m.viewerContentWidth(),
		m.viewport.SoftWrap,
	)
}

func (m *browserModel) captureViewerAnchor() viewerAnchor {
	if m.state != stateViewing || m.viewerDoc.content == "" {
		return viewerAnchor{}
	}
	if !m.viewport.SoftWrap {
		return viewerAnchor{line: m.viewport.YOffset(), cell: m.viewport.XOffset()}
	}
	return m.viewerLayout.anchorAt(m.viewport.YOffset())
}

func (m *browserModel) restoreViewerAnchor(anchor viewerAnchor) {
	if m.viewerDoc.content == "" {
		return
	}
	if m.viewport.SoftWrap {
		m.viewport.SoftWrap = false
		m.viewport.SetXOffset(0)
		m.viewport.SoftWrap = true
		m.viewport.SetYOffset(m.viewerLayout.yFor(anchor))
	} else {
		m.viewport.SetYOffset(anchor.line)
		m.viewport.SetXOffset(anchor.cell)
	}
	if len(m.viewerSearch.matches) > 0 {
		m.focusViewerMatch()
	}
}

func (m *browserModel) beginViewerSearch() tea.Cmd {
	m.viewerSearch.active = true
	m.viewerSearch.input.SetValue("")
	m.viewerSearch.input.CursorEnd()
	return m.viewerSearch.input.Focus()
}

func (m *browserModel) updateViewerSearch(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.viewerSearch.active = false
		m.viewerSearch.input.Blur()
		return m, nil
	case "enter":
		query := strings.TrimSpace(m.viewerSearch.input.Value())
		m.viewerSearch.active = false
		m.viewerSearch.input.Blur()
		m.applyViewerSearch(query)
		return m, nil
	}
	input, cmd := m.viewerSearch.input.Update(msg)
	m.viewerSearch.input = input
	return m, cmd
}

func smartLiteralMatches(content, query string, limit int) (matches [][]int, truncated bool) {
	if query == "" || limit <= 0 {
		return nil, false
	}
	pattern := regexp.QuoteMeta(query)
	caseSensitive := strings.ContainsFunc(query, unicode.IsUpper)
	if !caseSensitive {
		pattern = "(?i)" + pattern
	}
	re := regexp.MustCompile(pattern)
	matches = re.FindAllStringIndex(content, limit+1)
	if len(matches) > limit {
		return matches[:limit], true
	}
	return matches, false
}

func (m *browserModel) applyViewerSearch(query string) {
	yOffset, xOffset := m.viewport.YOffset(), m.viewport.XOffset()
	m.viewport.ClearHighlights()
	m.viewerSearch.query = query
	m.viewerSearch.matches = nil
	m.viewerSearch.index = -1
	m.viewerSearch.truncated = false
	if query == "" {
		return
	}

	matches, truncated := smartLiteralMatches(m.viewerDoc.content, query, maxViewerHighlights)
	m.viewerSearch.matches = matches
	m.viewerSearch.truncated = truncated
	if len(matches) == 0 {
		m.viewport.SetYOffset(yOffset)
		m.viewport.SetXOffset(xOffset)
		return
	}

	m.viewport.SetYOffset(0)
	if !m.viewport.SoftWrap {
		m.viewport.SetXOffset(0)
	}
	m.viewport.SetHighlights(matches)
	m.viewerSearch.index = 0
	m.focusViewerMatch()
}

func (m *browserModel) moveViewerHighlight(delta int) {
	count := len(m.viewerSearch.matches)
	if count == 0 || delta == 0 {
		return
	}
	if delta > 0 {
		m.viewport.HighlightNext()
		m.viewerSearch.index = (m.viewerSearch.index + 1) % count
	} else {
		m.viewport.HighlightPrevious()
		m.viewerSearch.index = (m.viewerSearch.index - 1 + count) % count
	}
	m.focusViewerMatch()
}

func (m *browserModel) focusViewerMatch() {
	if m.viewerSearch.index < 0 || m.viewerSearch.index >= len(m.viewerSearch.matches) {
		return
	}
	line, cell, virtualY := m.viewerLayout.matchPosition(
		m.viewerDoc.content,
		m.viewerSearch.matches[m.viewerSearch.index][0],
	)
	if m.viewport.SoftWrap {
		m.viewport.SetYOffset(virtualY)
		return
	}
	m.viewport.SetYOffset(line)
	width := m.viewerContentWidth()
	if cell < m.viewport.XOffset() || cell >= m.viewport.XOffset()+width {
		m.viewport.SetXOffset(max(0, cell-viewerHorizontalStep))
	}
}

func (m *browserModel) scrollViewer(delta int) {
	m.viewport.SetYOffset(m.viewport.YOffset() + delta)
}

func (m *browserModel) toggleViewerWrap() {
	if m.viewerDoc.content == "" {
		return
	}
	anchor := m.captureViewerAnchor()
	m.viewport.SoftWrap = !m.viewport.SoftWrap
	m.rebuildViewerLayout()
	m.restoreViewerAnchor(anchor)
}

func (m *browserModel) viewerWrapLabel() string {
	if m.viewport.SoftWrap {
		return "nowrap"
	}
	return "wrap"
}

func (m *browserModel) updateViewerMouse(msg tea.MouseWheelMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()
	if !m.viewport.SoftWrap && mouse.Mod.Contains(tea.ModShift) {
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.viewport.ScrollLeft(viewerHorizontalStep)
		case tea.MouseWheelDown:
			m.viewport.ScrollRight(viewerHorizontalStep)
		}
		return m, nil
	}
	switch mouse.Button {
	case tea.MouseWheelUp:
		m.scrollViewer(-m.viewport.MouseWheelDelta)
	case tea.MouseWheelDown:
		m.scrollViewer(m.viewport.MouseWheelDelta)
	case tea.MouseWheelLeft:
		if !m.viewport.SoftWrap {
			m.viewport.ScrollLeft(viewerHorizontalStep)
		}
	case tea.MouseWheelRight:
		if !m.viewport.SoftWrap {
			m.viewport.ScrollRight(viewerHorizontalStep)
		}
	}
	return m, nil
}
