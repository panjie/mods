package app

import (
	"fmt"
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
)

const planSystemPrompt = prompts.Plan

const maxPlanRetries = 3

type proposal struct {
	title   string
	content string
}

var proposalHeadingRe = regexp.MustCompile(`(?m)^#{2,}[ \t]+(?:Proposal[ \t]+\d+|方案[ \t]*\d+)[：:]`)

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
// requires: a "# Plan" / "# Proposal N" / "# 方案N" heading at any level, or the bold
// field labels (**Approach**, **Steps**, ...).
var planStructureRe = regexp.MustCompile(`(?m)(^#{1,}[ \t]+(?:Plan\b|Proposal\b|方案[ \t]*\d+)|\*\*(?:Approach|Steps|Files|Commands|Risks)\*\*)`)

// looksLikePlan reports whether the model's output actually contains a plan.
// It guards the plan-review UI: a stream that ended before the model wrote a
// plan (for example, when its investigation hit the tool-round limit) would
// otherwise render the review screen with whatever narration it produced.
func looksLikePlan(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" || !planStructureRe.MatchString(content) {
		return false
	}
	if proposals := parseProposals(content); len(proposals) > 0 {
		for _, proposal := range proposals {
			if !hasRequiredPlanFields(proposal.content) {
				return false
			}
		}
		return true
	}
	return hasRequiredPlanFields(content)
}

func hasRequiredPlanFields(content string) bool {
	fields := planFields(content)
	return fields["approach"] &&
		fields["steps"] &&
		fields["risks"] &&
		(fields["files"] || fields["commands"])
}

var planFieldRe = regexp.MustCompile(`(?mi)\*\*(Approach|Steps|Files|Commands|Risks)\*\*`)

func planFields(content string) map[string]bool {
	fields := map[string]bool{}
	for _, match := range planFieldRe.FindAllStringSubmatch(content, -1) {
		if len(match) > 1 {
			fields[strings.ToLower(match[1])] = true
		}
	}
	return fields
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
	block := renderInteractionPanel(m.Styles.Interaction, m.width, interactionPanel{
		Title:    "Plan ready",
		Meta:     fmt.Sprintf("Proposal %d/%d", m.proposalSelected+1, len(m.proposals)),
		Tone:     interactionToneInfo,
		Headline: "Select the proposal to use for execution",
		Actions: []interactionAction{
			{Key: "← →", Label: "Navigate"},
			{Key: "Y/Enter", Label: "Select"},
			{Key: "M", Label: "Modify"},
			{Key: "N", Label: "Try again"},
			{Key: "Ctrl+C", Label: "Cancel"},
		},
	})
	return appendInteractionPanel(content, block)
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
	actions := []interactionAction{
		{Key: "Y", Label: "Approve"},
		{Key: "N", Label: "Try again"},
		{Key: "M", Label: "Modify"},
		{Key: "Ctrl+C", Label: "Cancel"},
	}
	if m.planSelected < 0 || m.planSelected >= len(actions) {
		m.planSelected = 0
	}
	actions[m.planSelected].Selected = true
	block := renderInteractionPanel(m.Styles.Interaction, m.width, interactionPanel{
		Title:    "Plan ready",
		Tone:     interactionToneInfo,
		Headline: "Review the proposed plan before execution",
		Actions:  actions,
	})
	return appendInteractionPanel(content, block)
}

func (m *Mods) renderPlanFeedbackInput(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	innerWidth := interactionPanelInnerWidth(m.Styles.Interaction, m.width)
	// Reserve one cell for textinput's end-of-value cursor so the action row
	// stays at a stable vertical position while feedback is typed.
	m.feedbackInput.Width = max(1, innerWidth-m.Styles.Interaction.Input.GetHorizontalFrameSize()-3)
	m.feedbackInput.Prompt = ""
	block := renderInteractionPanel(m.Styles.Interaction, m.width, interactionPanel{
		Title:    "Modification feedback",
		Tone:     interactionToneInfo,
		Headline: "Describe the changes you want in the plan",
		Body:     []string{m.Styles.Interaction.Input.Render("› " + m.feedbackInput.View())},
		Actions:  []interactionAction{{Key: "Enter", Label: "Submit"}, {Key: "Esc", Label: "Cancel"}},
	})
	return appendInteractionPanel(content, block)
}

func appendInteractionPanel(content, panel string) string {
	if strings.TrimSpace(content) == "" {
		return panel
	}
	return strings.TrimRight(content, "\r\n") + "\n" + panel
}

// capturePlanHistory snapshots the non-system messages from the plan-phase
// conversation so they can be carried into the execution turn. System messages
// are excluded on purpose: setupStreamContext rebuilds the system block fresh
// for execution, and the plan-mode prompt ("PLANNING PHASE ONLY, do not
// execute") must not leak into the execution request.
func (m *Mods) capturePlanHistory() {
	m.planHistory = nil
	for _, msg := range m.messages {
		if msg.Role == proto.RoleSystem {
			continue
		}
		m.planHistory = append(m.planHistory, msg)
	}
}

// injectPlanHistory appends the captured plan-phase investigation to the
// execution request after setupStreamContext has rebuilt the system block.
// The leading user request is dropped because setupStreamContext just
// re-appended the fresh request (with current images/truncation); the
// remaining assistant turns, tool results, and proposed plan are carried so
// the model retains the context it gathered while planning instead of
// re-investigating from scratch.
func (m *Mods) injectPlanHistory() {
	if len(m.planHistory) == 0 {
		return
	}
	history := m.planHistory
	for len(history) > 0 && history[0].Role == proto.RoleUser {
		history = history[1:]
	}
	m.planHistory = nil
	m.messages = append(m.messages, history...)
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
