package main

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
	case requestState:
		if !m.Config.Quiet {
			return m.renderWithOperation(m.anim.View())
		}
	case responseState:
		if !m.Config.Raw && isOutputTTY() {
			if m.viewportNeeded() {
				return m.renderWithOperation(m.glamViewport.View())
			}
			// We don't need the viewport yet.
			return m.renderWithOperation(m.glamOutput)
		}

		if isOutputTTY() && !m.Config.Raw {
			return m.renderWithOperation(m.Output)
		}

		m.contentMutex.Lock()
		for _, c := range m.content {
			fmt.Print(c)
		}
		m.content = []string{}
		m.contentMutex.Unlock()
	case doneState:
		if !isOutputTTY() {
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
	if m.Config.Quiet || !m.showOperationStatus {
		return content
	}
	if strings.TrimSpace(m.activeOperation) == "" && !m.reasoningActive {
		return content
	}
	status := m.operationStatusLine()
	if content == "" {
		return status
	}
	return strings.TrimRight(content, "\r\n") + "\n" + status
}

func (m *Mods) operationStatusLine() string {
	text := m.activeOperation
	if text == "" && m.reasoningActive {
		text = "Reasoning..."
	}
	if m.reasoningActive {
		badge := m.Styles.InlineCode.Render("[R]")
		return badge + " " + m.Styles.Comment.Render(truncateOperationStatus(text, m.width-4))
	}
	text = truncateOperationStatus(text, m.width)
	return m.Styles.Comment.Render(text)
}

func (m *Mods) appendToOutput(s string) {
	m.Output += s
	if !isOutputTTY() || m.Config.Raw {
		m.contentMutex.Lock()
		m.content = append(m.content, s)
		m.contentMutex.Unlock()
		return
	}

	wasAtBottom := m.glamViewport.ScrollPercent() == 1.0
	oldHeight := m.glamHeight
	preprocessed := strings.ReplaceAll(m.Output, "\n\n", "\r")
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
