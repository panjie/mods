package app

import (
	"fmt"
	"os"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
)

const tabWidth = 4

type outputRenderer struct {
	Output               string
	glamOutput           string
	displayOutput        string
	outputBuilder        strings.Builder
	displayOutputBuilder strings.Builder
	displayBlocks        map[string]string
	displayBlockSeq      int
	glamHeight           int
	renderDirty          bool
	lastRenderFlush      time.Time
}

func (m *Mods) viewportNeeded() bool {
	return m.glamHeight > m.height
}

// View implements tea.Model.
func (m *Mods) View() tea.View {
	content := m.viewContent()
	view := tea.NewView(content)
	var panel ui.CursorView
	switch {
	case m.feedbackMode:
		panel = m.planFeedbackPanelView()
	case m.userInput.isPending():
		panel = m.userInput.renderView(m.width, m.Styles.Interaction)
	}
	if panel.Cursor != nil {
		cursor := *panel.Cursor
		cursor.Y += max(0, lipgloss.Height(content)-lipgloss.Height(panel.Content))
		view.Cursor = &cursor
	}
	return view
}

func (m *Mods) viewContent() string {
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
	op := strings.TrimSpace(m.getActiveOperation())
	if op != "" && !isShellCompletionStatus(op) {
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
	return m.anim.View().Content
}

func (m *Mods) operationStatusLine() string {
	text := m.getActiveOperation()
	text = TruncateOperationStatus(text, m.width)
	if line := m.shellCompletionStatusLine(text); line != "" {
		return line
	}
	if line := m.runningShellStatusLine(text); line != "" {
		return line
	}
	return statusBadge("RUNNING", m.Styles.Interaction.Warning) + m.Styles.Comment.Render(" "+text)
}

func (m *Mods) shellCompletionStatusLine(text string) string {
	switch {
	case strings.HasPrefix(text, "✓ "):
		return statusBadge("DONE", m.Styles.Interaction.Success) + m.Styles.Comment.Render(" "+strings.TrimPrefix(text, "✓ "))
	case strings.HasPrefix(text, "✗ "):
		return statusBadge("FAILED", m.Styles.Interaction.Danger) + m.Styles.Comment.Render(" "+strings.TrimPrefix(text, "✗ "))
	default:
		return ""
	}
}

func isShellCompletionStatus(text string) bool {
	return strings.HasPrefix(text, "✓ ") || strings.HasPrefix(text, "✗ ")
}

func (m *Mods) runningShellStatusLine(text string) string {
	if _, _, _, ok := runningShellStatus(text); !ok {
		return ""
	}
	return statusBadge("RUNNING", m.Styles.Interaction.Warning) + m.Styles.Comment.Render(" "+text)
}

func statusBadge(label string, style lipgloss.Style) string {
	return style.Render(fmt.Sprintf("%-7s", label))
}

func runningShellStatus(text string) (prefix, command, suffix string, ok bool) {
	for _, marker := range []string{"Shell - ", "PS - ", "Shell: ", "PS: "} {
		if after, found := strings.CutPrefix(text, marker); found {
			command, suffix, _ = strings.Cut(after, " - last: ")
			if suffix != "" {
				suffix = " - last: " + suffix
			}
			return marker, strings.TrimSpace(command), suffix, true
		}
	}
	return "", "", "", false
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

// toolResultOutputCmd emits a compact one-line activity record for a completed
// tool call. It deliberately bypasses every response buffer so stdout and
// RenderedOutput remain model-output-only. When Bubble Tea owns stderr, Println
// inserts the record safely above the live renderer; otherwise write it
// directly to stderr.
//
// HideToolStatus suppresses these records too: the flag's name promises to hide
// tool status, and the completed-call summary is part of that surface (the
// footer's running label is gated separately via showOperationStatus).
func (m *Mods) toolResultOutputCmd(name string, data []byte, err error) tea.Cmd {
	if m.Config.Raw || m.Config.Minimal || m.Config.HideToolStatus {
		return nil
	}
	status := ToolResultStatus(name, data, err, m.toolResultStatusWidth())
	if status == "" {
		return nil
	}
	if m.secrets != nil {
		status = m.secrets.Redact(status)
	}
	if m.Config.InteractiveTTYAvailable && IsErrorTTY() {
		return tea.Println(m.toolResultDisplayLine(status))
	}
	_, _ = fmt.Fprintln(os.Stderr, status)
	return nil
}

func (m *Mods) toolResultStatusWidth() int {
	width := m.width
	if m.Config != nil && m.Config.WordWrap > 0 && (width <= 0 || m.Config.WordWrap < width) {
		width = m.Config.WordWrap
	}
	if width <= 0 {
		width = 80
	}
	// The TTY activity line has a four-cell rail ("  │ "). Leave room for it so
	// the result always remains a single physical line.
	return max(1, width-4)
}

func (m *Mods) toolResultDisplayLine(status string) string {
	const rail = "  │ "
	marker := ""
	switch {
	case strings.HasPrefix(status, "✓ "):
		marker = "✓"
	case strings.HasPrefix(status, "✗ "):
		marker = "✗"
	}
	if marker == "" {
		return m.Styles.Comment.Render(rail + status)
	}
	markerStyle := m.Styles.Interaction.Success
	if marker == "✗" {
		markerStyle = m.Styles.Interaction.Danger
	}
	return m.Styles.Comment.Render(rail) + markerStyle.Render(marker) + m.Styles.Comment.Render(status[len(marker):])
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

func (m *Mods) appendToOutputWithDisplayBlock(raw, block string) {
	marker := m.nextDisplayBlockMarker(block)
	m.appendToOutputWithDisplay(raw, marker+"\n\n")
}

func (m *Mods) nextDisplayBlockMarker(block string) string {
	if m.displayBlocks == nil {
		m.displayBlocks = map[string]string{}
	}
	m.displayBlockSeq++
	marker := fmt.Sprintf("MODS_DISPLAY_BLOCK_%d", m.displayBlockSeq)
	m.displayBlocks[marker] = block
	return marker
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
	m.glamOutput = m.replaceDisplayBlocks(m.glamOutput)
	m.glamHeight = lipgloss.Height(m.glamOutput)
	m.glamOutput += "\n"
	content := m.glamOutput
	if m.width > 0 {
		content = lipgloss.NewStyle().
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

func (m *Mods) replaceDisplayBlocks(rendered string) string {
	if len(m.displayBlocks) == 0 {
		return rendered
	}
	lines := strings.Split(rendered, "\n")
	changed := false
	for i, line := range lines {
		marker := strings.TrimSpace(ansi.Strip(line))
		block, ok := m.displayBlocks[marker]
		if !ok {
			continue
		}
		lines[i] = block
		changed = true
	}
	if !changed {
		return rendered
	}
	return strings.Join(lines, "\n")
}

func (m *Mods) resetOutputBuffers() {
	m.Output = ""
	m.displayOutput = ""
	m.outputBuilder.Reset()
	m.displayOutputBuilder.Reset()
	m.displayBlocks = nil
	m.displayBlockSeq = 0
	m.renderDirty = false
	m.lastRenderFlush = time.Time{}
}

// flushThought renders the accumulated reasoning/thinking content before the
// answer. Raw output keeps the explicit markdown separator for compatibility,
// while the TTY display uses the same panel language as runtime prompts.
func (m *Mods) flushThought() {
	m.thoughtFlushed = true
	thought := strings.TrimSpace(m.Thought)
	if thought == "" {
		return
	}
	m.appendToOutputWithDisplayBlock(thoughtMarkdown(thought), thoughtDisplayBlock(m.Styles.Interaction, m.width, thought))
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

func thoughtDisplayBlock(styles ui.InteractionStyles, width int, thought string) string {
	return renderInteractionPanel(styles, width, interactionPanel{
		Title: "Thinking",
		Meta:  "-t",
		Tone:  interactionToneInfo,
		Body:  strings.Split(thought, "\n"),
	})
}
