package app

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

const tabWidth = 4

type outputRenderer struct {
	Output               string
	glamOutput           string
	displayOutput        string
	outputBuilder        strings.Builder
	displayOutputBuilder strings.Builder
	glamHeight           int
	renderDirty          bool
	lastRenderFlush      time.Time
}

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
		m.flushRender()
		if m.feedbackMode {
			content := m.glamOutput
			if content == "" {
				content = m.Output
			}
			return m.renderPlanFeedbackInput(content)
		}
		if m.proposalMode && len(m.proposals) > 0 {
			content := m.glamOutput
			if content == "" {
				content = m.Output
			}
			if m.viewportNeeded() {
				return m.renderProposalSelectionBar(m.glamViewport.View())
			}
			return m.renderProposalSelectionBar(content)
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
		m.flushRender()
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

		m.flushBufferedContent()
	case doneState:
		m.flushBufferedContent()
		if !IsOutputTTY() {
			fmt.Printf("\n")
		}
		return ""
	}
	return ""
}

func (m *Mods) flushBufferedContent() {
	m.contentMutex.Lock()
	defer m.contentMutex.Unlock()
	for _, c := range m.content {
		fmt.Print(c)
	}
	m.content = []string{}
}

func (m *Mods) renderWithOperation(content string) string {
	footer := m.footerView()
	if footer == "" {
		return content
	}
	// A non-empty footer means active tool activity (a running tool or a
	// pending approval), not generation. While the model hasn't emitted any
	// text yet, `content` is just the "Generating…" spinner — drop it so the
	// spinner can never appear alongside the footer. This is what makes the
	// status surfaces mutually exclusive by construction.
	//
	// Safe because View() passes real model output as `content` only once
	// responseOutputStarted is true (the pre-output branch passes the spinner);
	// so when this gate fires, `content` is the spinner — never output.
	if !m.responseOutputStarted {
		content = ""
	}
	if strings.TrimSpace(content) == "" {
		return footer
	}
	return strings.TrimRight(content, "\r\n") + "\n" + footer
}

// footerView is the single source of truth for the status line shown beneath
// the main content. At most one footer is ever returned: a pending tool
// approval takes precedence over the current tool/web-search operation label.
// renderWithOperation composes the footer below the main content and suppresses
// the "Generating…" spinner whenever a footer is active.
func (m *Mods) footerView() string {
	if m.reviewer.isPending() {
		return m.reviewer.renderBanner(m.width, m.Styles.ReviewPrompt, m.Styles.ReviewChoices)
	}
	if m.Config.Quiet || m.Config.HideToolStatus || !m.showOperationStatus {
		return ""
	}
	if strings.TrimSpace(m.getActiveOperation()) == "" {
		return ""
	}
	return m.operationStatusLine()
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

// appendShellResult appends a compact transcript block describing the outcome of
// a completed shell command so users can review which commands ran and whether
// they succeeded. Non-shell tools render as empty and are skipped.
func (m *Mods) appendShellResult(name string, data []byte, err error) {
	if m.Config.HideToolResults {
		return
	}
	block := ShellResultBlock(name, data, err)
	if block == "" {
		return
	}
	if m.Output == "" {
		m.appendToOutput(block + "\n\n")
		return
	}
	m.appendToOutput("\n\n" + block + "\n\n")
}

func (m *Mods) appendToOutputWithDisplay(raw, display string) {
	m.outputBuilder.WriteString(raw)
	m.Output = m.outputBuilder.String()
	m.displayOutputBuilder.WriteString(display)
	m.displayOutput = m.displayOutputBuilder.String()
	if !IsOutputTTY() || m.Config.Raw {
		m.contentMutex.Lock()
		m.content = append(m.content, raw)
		m.contentMutex.Unlock()
		return
	}

	m.renderDirty = true
	if !m.shouldFlushRender(display) {
		return
	}
	m.flushRender()
}

func (m *Mods) shouldFlushRender(display string) bool {
	if strings.ContainsAny(display, "\n\r") {
		return true
	}
	if m.lastRenderFlush.IsZero() {
		return true
	}
	return time.Since(m.lastRenderFlush) >= renderFrameInterval
}

func (m *Mods) flushRender() {
	if !m.renderDirty || !IsOutputTTY() || m.Config.Raw {
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
	m.renderDirty = false
	m.lastRenderFlush = time.Now()
}

func (m *Mods) resetOutputBuffers() {
	m.Output = ""
	m.displayOutput = ""
	m.outputBuilder.Reset()
	m.displayOutputBuilder.Reset()
	m.renderDirty = false
	m.lastRenderFlush = time.Time{}
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
