package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

const browserPreviewMinWidth = 110

type previewLoadedMsg struct{ loadedContentMsg }

func (m *browserModel) splitPreview() bool {
	return m.width >= browserPreviewMinWidth && m.height >= 12
}
func (m *browserModel) listWidth() int {
	if m.splitPreview() && m.state == stateBrowsing {
		return min(48, m.width/3)
	}
	return m.width
}

// Preview requests are versioned separately from the full transcript viewer.
// Rapid selection/filter changes cannot attach an old response to a new row.
func (m *browserModel) refreshPreview() tea.Cmd {
	if m.state != stateBrowsing || !m.splitPreview() {
		return nil
	}
	ci, ok := m.focused()
	if !ok {
		m.previewID = ""
		m.previewText = ""
		return nil
	}
	if ci.conv.ID == m.previewID {
		return nil
	}
	m.previewID = ci.conv.ID
	m.previewRequest++
	m.previewText, m.previewErr = "", ""
	m.previewLoaded = false
	if m.db == nil {
		return nil
	}
	cmd := m.loadContent(ci.conv.ID, m.previewRequest)
	return func() tea.Msg { return previewLoadedMsg{cmd().(loadedContentMsg)} }
}

func (m *browserModel) receivePreview(msg previewLoadedMsg) {
	if msg.id != m.previewID || msg.request != m.previewRequest {
		return
	}
	m.previewLoaded = true
	if msg.err != nil {
		m.previewErr = plainTranscriptContent(msg.err.Error())
		return
	}
	var text strings.Builder
	for _, message := range msg.messages {
		fmt.Fprintf(&text, "%s\n%s\n\n", message.Role, readableMessageContent(message))
		if text.Len() > 16384 {
			break
		}
	}
	m.previewText = text.String()
	if len(m.previewText) > 16384 {
		m.previewText = strings.ToValidUTF8(m.previewText[:16384], "")
	}
}

func (m *browserModel) viewPreview(width, height int) string {
	ci, ok := m.focused()
	if !ok {
		return lipgloss.NewStyle().Width(width).Height(height).Render("No matching sessions.")
	}
	title := strings.Join(strings.Fields(plainTranscriptContent(ci.conv.Title)), " ")
	if title == "" {
		title = "(untitled)"
	}
	lines := []string{m.styles.viewerTitle.Render(ansi.Truncate(title, width, "…")), ""}
	body := m.previewText
	switch {
	case m.previewErr != "":
		body = "Could not load preview.\nPress Enter to open the session or r to retry.\n\n" + m.previewErr
	case !m.previewLoaded:
		body = "Loading preview…"
	case strings.TrimSpace(body) == "":
		body = "This session has no messages."
	}
	wrapped := strings.Split(ansi.Hardwrap(body, max(1, width), false), "\n")
	available := max(0, height-len(lines)-2)
	if len(wrapped) > available {
		wrapped = wrapped[:available]
	}
	lines = append(lines, wrapped...)
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	lines = append(lines, m.styles.empty.Render("Enter opens full session"))
	for i := range lines {
		lines[i] = ansi.Truncate(lines[i], width, "…")
	}
	return lipgloss.NewStyle().Width(width).Height(height).Render(strings.Join(lines, "\n"))
}

func (m *browserModel) viewKeyboardHelp() string {
	text := "Keyboard shortcuts\n\nSession list\n↑↓ / j k   Move\n/          Filter sessions\nEnter      Open full session\nc          Continue conversation\nSpace      Mark session\nd / D      Delete focused / marked sessions\nr          Retry preview\nq          Quit\n\nSession viewer\n↑↓ / j k   Scroll\nPgUp/PgDn  Page\n/          Find text\nn / N      Next / previous match\nw          Toggle wrapping\nc          Continue conversation\nC          Copy session\nr          Retry failed load\nEsc / q    Back to list"
	lines := strings.Split(ansi.Wrap(text, max(1, m.width), ""), "\n")
	visible := max(1, m.height-2)
	m.helpOffset = min(m.helpOffset, max(0, len(lines)-visible))
	body := strings.Join(lines[m.helpOffset:min(len(lines), m.helpOffset+visible)], "\n")
	return lipgloss.NewStyle().Height(visible).Render(body) + "\n" + ansi.Truncate("↑↓ scroll · Esc back", max(1, m.width), "…")
}
