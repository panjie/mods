package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
)

const planSystemPrompt = `You are in PLAN mode. Before executing anything, you must first create a detailed, step-by-step plan for the user to review.

You have access to read-only tools (file reading, directory listing, searching, web search) to inspect the codebase. Use them freely to understand the code.

Think through the task carefully and include:
- What files need to be created or modified
- What commands need to be run
- What changes need to be made
- Any potential issues, edge cases, or risks
- The order in which steps should be executed

Output your plan in plain text. Do NOT request any tool execution during this phase.
The user will review your plan and approve or deny it before execution begins.`

const maxPlanRetries = 3

func (m *Mods) setupPlanContext(content string, mod Model) error {
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
		session, err := m.buildRequestSession(content, requestModePlan)
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
	baseStyle := m.Styles.ReviewChoices.Copy().Padding(0, 0)
	selectedStyle := baseStyle.Copy().
		Foreground(lipgloss.Color("#4A3B9F")).
		Background(lipgloss.Color("#E0DDFF"))
	options := []string{
		"[Y] Approve",
		"[N] Try again",
		"[M] Modify",
		"[Ctrl+C] Cancel",
	}
	var parts []string
	for i, opt := range options {
		if i == m.planSelected {
			parts = append(parts, selectedStyle.Render(opt))
		} else {
			parts = append(parts, baseStyle.Render(opt))
		}
	}
	choicesLine := m.Styles.ReviewChoices.Copy().Width(m.width).Render(strings.Join(parts, "  "))
	block := promptLine + "\n" + choicesLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
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
