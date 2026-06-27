package app

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
)

const planSystemPrompt = `You are in PLAN mode. Before executing anything, you must first create a detailed, step-by-step plan for the user to review.

CRITICAL — PLANNING PHASE ONLY. You are NOT authorized to:

- Write any scripts or programs (Python, shell, JS, etc.) — even "temporary" or "experimental" ones
- Create or modify any files anywhere (workspace, /tmp, or safe workspace)
- Run any self-written code or script
- Execute commands that produce the task's final output

Investigation means READING, not BUILDING. If you catch yourself writing a script, STOP — that script belongs in the plan, not in your current tool calls.

Valid investigation means read-only inspection. Use platform-appropriate read-only commands for listing directories, reading files, searching text, and checking metadata; do not redirect output to files. Built-in read-only tools such as fs_list_dir, fs_read_file, fs_stat, and fs_search are allowed.

When you have enough context, output the plan IMMEDIATELY. Do not over-investigate. Do not include investigation notes, tool call results, or running commentary — just the plan itself.

## Output Format

### Single approach — use this heading and structure:

## Plan
- **Approach**: one-line summary of the strategy
- **Steps**: numbered list of actions in execution order
- **Files**: files that will be created or modified, one per line with a brief note
- **Commands**: shell commands that will be run, one per line
- **Risks**: potential issues, edge cases, or limitations

### Multiple approaches — use this structure for each:

## Proposal 1: Brief Title
- **Approach**: ...
- **Steps**: ...
- **Files**: ...
- **Commands**: ...
- **Risks**: ...

## Proposal 2: Brief Title
- **Approach**: ...
- **Steps**: ...
- **Files**: ...
- **Commands**: ...
- **Risks**: ...

Each proposal must be self-contained and independently actionable.

Each proposal heading MUST begin with exactly two hash characters followed by a space (for example, "## Proposal 1: Title"). Do not use three or more hash characters and do not nest proposals under another heading; the proposal parser recognizes proposals only at this exact heading level.

The user will review your plan and approve or deny it before execution begins.`

const maxPlanRetries = 3

type proposal struct {
	title   string
	content string
}

var proposalHeadingRe = regexp.MustCompile(`(?m)^#{2,}[ \t]+Proposal[ \t]+\d+:`)

func parseProposals(content string) []proposal {
	locs := proposalHeadingRe.FindAllStringIndex(content, -1)
	if len(locs) < 2 {
		return nil
	}
	proposals := make([]proposal, 0, len(locs))
	for i, loc := range locs {
		start := loc[0]
		end := len(content)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		raw := strings.TrimSpace(content[start:end])
		titleLine, body, _ := strings.Cut(raw, "\n")
		title := strings.TrimSpace(strings.TrimLeft(titleLine, "#"))
		proposals = append(proposals, proposal{
			title:   title,
			content: strings.TrimSpace(body),
		})
	}
	return proposals
}

// planStructureRe matches the structural markers the plan system prompt
// requires: a "# Plan" / "# Proposal N" heading at any level, or the bold
// field labels (**Approach**, **Steps**, ...).
var planStructureRe = regexp.MustCompile(`(?m)(^#{1,}[ \t]+(?:Plan|Proposal)\b|\*\*(?:Approach|Steps|Files|Commands|Risks)\*\*)`)

// looksLikePlan reports whether the model's output actually contains a plan.
// It guards the plan-review UI: a stream that ended before the model wrote a
// plan (for example, when its investigation hit the tool-round limit) would
// otherwise render the review screen with whatever narration it produced.
func looksLikePlan(content string) bool {
	return strings.TrimSpace(content) != "" && planStructureRe.MatchString(content)
}

func (m *Mods) showProposal(idx int) {
	if idx < 0 || idx >= len(m.proposals) {
		return
	}
	m.proposalSelected = idx
	content := "## " + m.proposals[idx].title + "\n\n" + m.proposals[idx].content
	m.planContent = content
	m.resetOutputBuffers()
	m.appendToOutput(content)
}

func (m *Mods) renderProposalSelectionBar(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	headerLine := m.Styles.ReviewPrompt.Copy().Width(m.width).Render(
		padRight("  Plan Review - Select a Proposal", m.width-4),
	)
	navLabel := fmt.Sprintf("Proposal %d/%d", m.proposalSelected+1, len(m.proposals))
	options := []string{
		"[← →] Navigate",
		"[Y] Select",
		"[M] Modify",
		"[N] Try again",
		"[Ctrl+C] Cancel",
	}
	navStyle := m.Styles.ReviewChoices.Copy().Padding(0, 0)
	optsLine := m.renderReviewOptions(options, m.planSelected, navStyle)
	navLine := navStyle.Copy().Width(m.width).Render(
		navStyle.Render(navLabel) + "  " + optsLine,
	)
	block := headerLine + "\n" + navLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func (m *Mods) setupPlanContext(content string, mod Model) error {
	wasPlan := m.Config.Plan
	m.Config.Plan = true
	defer func() { m.Config.Plan = wasPlan }()

	if err := m.setupStreamContext(content, mod); err != nil {
		return err
	}
	planMsg := proto.Message{
		Role:    proto.RoleSystem,
		Content: planSystemPrompt,
	}
	if len(m.messages) > 0 && m.messages[0].Role == proto.RoleSystem {
		m.messages = append(
			m.messages[:1],
			append([]proto.Message{planMsg}, m.messages[1:]...)...,
		)
	} else {
		m.messages = append([]proto.Message{planMsg}, m.messages...)
	}
	return nil
}

func (m *Mods) startPlanCmd(content string) tea.Cmd {
	m.cancelMu.Lock()
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	m.responseOutputStarted = false
	m.responseBoundaryPending = false
	m.resetOutputBuffers()

	return func() tea.Msg {
		session, err := m.buildRequestSession(content)
		if err != nil {
			return err
		}
		return session.runner.receiveCmd()()
	}
}

func (m *Mods) renderPlanReviewBanner(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	promptLine := m.Styles.ReviewPrompt.Copy().Width(m.width).Render(
		padRight("  Plan Review", m.width-4),
	)
	options := []string{
		"[Y] Approve",
		"[N] Try again",
		"[M] Modify",
		"[Ctrl+C] Cancel",
	}
	baseStyle := m.Styles.ReviewChoices.Copy().Padding(0, 0)
	choicesLine := m.Styles.ReviewChoices.Copy().Width(m.width).Render(
		m.renderReviewOptions(options, m.planSelected, baseStyle),
	)
	block := promptLine + "\n" + choicesLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func (m *Mods) renderReviewOptions(options []string, selected int, base lipgloss.Style) string {
	highlight := base.Copy().
		Foreground(lipgloss.Color("#4A3B9F")).
		Background(lipgloss.Color("#E0DDFF"))
	if selected >= len(options) {
		selected = 0
	}
	var parts []string
	for i, opt := range options {
		if i == selected {
			parts = append(parts, highlight.Render(opt))
		} else {
			parts = append(parts, base.Render(opt))
		}
	}
	return strings.Join(parts, "  ")
}

func (m *Mods) renderPlanFeedbackInput(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	promptLine := m.Styles.ReviewPrompt.Copy().Width(m.width).Render(
		padRight("  Modification Feedback", m.width-4),
	)
	inputLine := m.Styles.ReviewChoices.Copy().Width(m.width).Render(
		"  " + m.feedbackInput.View(),
	)
	block := promptLine + "\n" + inputLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func (m *Mods) approvedPlanTranscript() string {
	content := m.glamOutput
	if content == "" {
		content = m.Output
	}
	content = strings.TrimRight(content, "\r\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return content + "\n"
}

func (m *Mods) approvedPlanPrintCmd(transcript string) tea.Cmd {
	if transcript == "" || m.Config.Raw || !isOutputTTY() {
		return nil
	}
	return tea.Println(transcript)
}

func (m *Mods) resetExecutionOutput() {
	m.resetOutputBuffers()
	m.glamOutput = ""
	m.glamHeight = 0
	m.glamViewport.SetContent("")
	m.responseOutputStarted = false
	m.responseBoundaryPending = false
}
