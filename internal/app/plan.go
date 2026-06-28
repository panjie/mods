package app

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
)

const planSystemPrompt = prompts.Plan

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
		"[Y/Enter] Select",
		"[M] Modify",
		"[N] Try again",
		"[Ctrl+C] Cancel",
	}
	navStyle := m.Styles.ReviewChoices.Copy().Padding(0, 0)
	// Render options as plain badges: every option maps to a direct key, so
	// highlighting a single one based on m.planSelected (which has no key
	// to advance it in proposal mode) would mislead the user.
	optsLine := navStyle.Render(strings.Join(options, "  "))
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
	planPrompt, err := m.resolvePrompt(prompts.KeyPlan, planSystemPrompt)
	if err != nil {
		return err
	}
	planMsg := proto.Message{
		Role:    proto.RoleSystem,
		Content: planPrompt,
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
	// Release any prior session's resources first so a retry/replan does
	// not leave an HTTP stream or tool registry leaking behind.
	m.closeActiveRunner()
	m.cancelMu.Lock()
	cancels := m.cancelRequest
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
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
