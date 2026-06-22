package app

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

const tabWidth = 4

func (m *Mods) viewportNeeded() bool {
	return m.glamHeight > m.height
}

// View implements tea.Model.
func (m *Mods) View() string {
	//nolint:exhaustive
	switch m.state {
	case errorState:
		return ""
	case planState:
		if m.feedbackMode {
			content := m.glamOutput
			if content == "" {
				content = m.Output
			}
			return m.renderPlanFeedbackInput(content)
		}
		if strings.TrimSpace(m.planContent) != "" {
			content := m.glamOutput
			if content == "" {
				content = m.Output
			}
			if m.viewportNeeded() {
				return m.renderPlanReviewBanner(m.glamViewport.View())
			}
			return m.renderPlanReviewBanner(content)
		}
		if !m.Config.Quiet && !debug.Enabled() {
			return m.renderWithOperation(m.anim.View())
		}
	case requestState:
		if !m.Config.Quiet && !debug.Enabled() {
			return m.renderWithOperation(m.anim.View())
		}
	case responseState:
		if !m.Config.Quiet && !debug.Enabled() && !m.responseOutputStarted && m.anim != nil {
			return m.renderWithOperation(m.anim.View())
		}
		if !m.Config.Raw && IsOutputTTY() {
			if m.viewportNeeded() {
				return m.renderWithOperation(m.glamViewport.View())
			}
			// We don't need the viewport yet.
			return m.renderWithOperation(m.glamOutput)
		}

		if IsOutputTTY() && !m.Config.Raw {
			return m.renderWithOperation(m.Output)
		}

		m.contentMutex.Lock()
		for _, c := range m.content {
			fmt.Print(c)
		}
		m.content = []string{}
		m.contentMutex.Unlock()
	case doneState:
		if !IsOutputTTY() {
			fmt.Printf("\n")
		}
		return ""
	}
	return ""
}

func (m *Mods) renderWithOperation(content string) string {
	if m.reviewer.isPending() {
		return m.reviewer.renderBanner(content, m.width, m.Styles.ReviewPrompt, m.Styles.ReviewChoices)
	}
	if m.Config.Quiet || m.Config.HideToolStatus || !m.showOperationStatus {
		return content
	}
	if strings.TrimSpace(m.getActiveOperation()) == "" {
		return content
	}
	status := m.operationStatusLine()
	if content == "" {
		return status
	}
	return strings.TrimRight(content, "\r\n") + "\n" + status
}

func (m *Mods) operationStatusLine() string {
	text := m.getActiveOperation()
	text = TruncateOperationStatus(text, m.width)
	return m.Styles.Comment.Render(text)
}

func (m *Mods) appendToOutput(s string) {
	m.appendToOutputWithDisplay(s, s)
}

func (m *Mods) appendResponseBoundary() {
	if m.Output == "" || strings.HasSuffix(m.Output, "\n") || strings.HasSuffix(m.Output, "\r") {
		return
	}
	m.appendToOutput("\n\n")
}

func (m *Mods) appendToOutputWithDisplay(raw, display string) {
	m.Output += raw
	m.displayOutput += display
	if !IsOutputTTY() || m.Config.Raw {
		m.contentMutex.Lock()
		m.content = append(m.content, raw)
		m.contentMutex.Unlock()
		return
	}

	wasAtBottom := m.glamViewport.ScrollPercent() == 1.0
	oldHeight := m.glamHeight
	preprocessed := strings.ReplaceAll(m.displayOutput, "\n\n", "\r")
	preprocessed = strings.ReplaceAll(preprocessed, "\n", "  \n")
	preprocessed = strings.ReplaceAll(preprocessed, "\r", "\n\n")
	m.glamOutput, _ = m.glam.Render(preprocessed)
	m.glamOutput = strings.TrimRightFunc(m.glamOutput, unicode.IsSpace)
	m.glamOutput = strings.ReplaceAll(m.glamOutput, "\t", strings.Repeat(" ", tabWidth))
	m.glamHeight = lipgloss.Height(m.glamOutput)
	m.glamOutput += "\n"
	content := m.glamOutput
	if m.width > 0 {
		content = m.renderer.NewStyle().
			MaxWidth(m.width).
			Render(m.glamOutput)
	}
	m.glamViewport.SetContent(content)
	if oldHeight < m.glamHeight && wasAtBottom {
		// If the viewport's at the bottom and we've received a new
		// line of content, follow the output by auto scrolling to
		// the bottom.
		m.glamViewport.GotoBottom()
	}
}

// flushThought renders the accumulated reasoning/thinking content before the
// answer. Raw output keeps the explicit markdown separator for compatibility,
// while the TTY display uses a quieter block.
func (m *Mods) flushThought() {
	m.thoughtFlushed = true
	thought := strings.TrimSpace(m.Thought)
	if thought == "" {
		return
	}
	m.appendToOutputWithDisplay(thoughtMarkdown(thought), thoughtDisplayMarkdown(thought))
}

// thoughtMarkdown formats reasoning content as a labelled markdown
// blockquote followed by a horizontal rule.
func thoughtMarkdown(thought string) string {
	var b strings.Builder
	b.WriteString("> **💭 thinking**\n>\n")
	for _, line := range strings.Split(thought, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(">\n")
			continue
		}
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n---\n\n")
	return b.String()
}

// thoughtDisplayMarkdown formats reasoning content for interactive terminals.
// It stays in markdown so glamour can apply the user's theme, but keeps the
// block visually quiet and leaves the final answer as the main event.
func thoughtDisplayMarkdown(thought string) string {
	var b strings.Builder
	b.WriteString("> _thinking_\n>\n")
	for _, line := range strings.Split(thought, "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteString(">\n")
			continue
		}
		b.WriteString("> ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
