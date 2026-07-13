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
		if !debug.Enabled() {
			return m.renderWithOperation("")
		}
	case requestState:
		if !debug.Enabled() {
			return m.renderWithOperation("")
		}
	case responseState:
		m.flushRender()
		if !m.Config.Raw && !IsOutputTTY() && m.reviewer.isPending() && m.reviewer.canReviewInteractively() {
			m.flushBufferedContent()
			return m.renderWithOperation("")
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
	if strings.TrimSpace(content) == "" {
		return footer
	}
	return strings.TrimRight(content, "\r\n") + "\n" + footer
}

// footerView is the single source of truth for the bottom status line. It is
// the union of two independent surfaces:
//
//  1. The operation-status label ("Running: …") — gated by showOperationStatus
//     (which encodes TTY + !raw + !hide-tool-status in production), exactly as
//     before. This keeps showing even if the spinner itself can't render.
//  2. The always-on spinner — gated by spinnerVisible (running phase, TTY,
//     non-debug, anim present). Its palette reflects the current phase.
//
// A pending tool approval takes over the footer with its review banner (that is
// a "waiting on the user" state, not a running phase, so the spinner pauses).
// In the Tool phase both surfaces are active and composed as "spinner  label".
func (m *Mods) footerView() string {
	if m.userInput.isPending() {
		return m.userInput.render(m.width, m.Styles.Interaction)
	}
	if m.reviewer.isPending() {
		return m.reviewer.renderBanner(m.width, m.Styles.Interaction)
	}

	var opLine string
	if m.showOperationStatus && !m.Config.HideToolStatus {
		if op := strings.TrimSpace(m.getActiveOperation()); op != "" {
			opLine = m.operationStatusLine()
		}
	}

	if m.spinnerVisible() {
		spin := m.spinnerLine()
		if spin == "" {
			return opLine
		}
		if opLine != "" {
			return spin + " " + opLine
		}
		return spin
	}
	return opLine
}

// spinnerPhase derives the animation palette from the current runtime state.
// The three phases are mutually exclusive in time: a tool/search operation is
// only "active" while the model is paused waiting for its result, and
// responseOutputStarted flips true once the first token of an answer streams.
// Thinking (-t) is treated as Streaming since the model is producing tokens.
func (m *Mods) spinnerPhase() SpinnerPhase {
	if strings.TrimSpace(m.getActiveOperation()) != "" {
		return PhaseTool
	}
	if m.responseOutputStarted || m.thinkActive {
		return PhaseStreaming
	}
	return PhaseConnecting
}

// spinnerVisible reports whether the bottom spinner should render right now.
// It is false for terminal states, plan review (banner surface), pending
// approval, debug, raw, and non-TTY — all the cases that previously suppressed
// the spinner. It is the single shared gate for both rendering and ticking.
func (m *Mods) spinnerVisible() bool {
	if debug.Enabled() || m.Config.Raw || !IsOutputTTY() || m.anim == nil || m.userInput.isPending() {
		return false
	}
	switch m.state {
	case doneState, errorState:
		return false
	case planState:
		// Plan review shows its own banner; the spinner only shows while the
		// plan is still being generated (no planContent yet).
		return strings.TrimSpace(m.planContent) == ""
	default:
		return true
	}
}

// spinnerLine renders the phase-colored spinner, pushing the derived phase into
// the Anim so its ramp uses the right palette. The Anim is stored as tea.Model
// (to allow test fakes), so this type-asserts to the concrete Anim; test fakes
// fall through and render via their own View. The reassigment persists the
// rebuilt ramp so the color-cycling rotation in Update keeps the new colors.
func (m *Mods) spinnerLine() string {
	phase := m.spinnerPhase()
	if a, ok := m.anim.(Anim); ok {
		a.SetPhase(phase)
		m.anim = a
	}
	return m.anim.View()
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
	if !m.Config.ShowToolResults {
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
