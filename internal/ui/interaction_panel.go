package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

type InteractionTone int

const (
	InteractionToneInfo InteractionTone = iota
	InteractionToneWarning
	InteractionToneDanger
	InteractionToneSuccess
)

type InteractionRow struct {
	Label string
	Value string
}

type InteractionAction struct {
	Key      string
	Label    string
	Selected bool
}

type InteractionPanel struct {
	Title    string
	Meta     string
	Tone     InteractionTone
	ToneText string
	Headline string
	Rows     []InteractionRow
	Body     []string
	Choices  []InteractionAction
	Actions  []InteractionAction
}

func RenderInteractionPanel(styles InteractionStyles, width int, panel InteractionPanel) string {
	if width <= 0 {
		width = 80
	}
	panelStyle := styles.Panel.Copy().BorderForeground(interactionToneColor(styles, panel.Tone))
	innerWidth := max(1, width-panelStyle.GetHorizontalFrameSize())
	lines := []string{renderInteractionTitle(styles, innerWidth, panel.Tone, panel.Title, panel.Meta)}
	if panel.Headline != "" || panel.ToneText != "" {
		lines = append(lines, renderInteractionHeadline(styles, innerWidth, panel.Tone, panel.ToneText, panel.Headline)...)
	}
	for _, row := range panel.Rows {
		lines = append(lines, renderInteractionRow(styles, innerWidth, row)...)
	}
	for _, body := range panel.Body {
		if body == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, ansi.Hardwrap(body, innerWidth, false))
	}
	if len(panel.Choices) > 0 {
		lines = append(lines, "")
		lines = append(lines, packInteractionActions(styles, innerWidth, panel.Choices)...)
	}
	if len(panel.Actions) > 0 {
		lines = append(lines, "")
		lines = append(lines, packInteractionActions(styles, innerWidth, panel.Actions)...)
	}
	return panelStyle.Render(strings.Join(lines, "\n"))
}

func InteractionPanelInnerWidth(styles InteractionStyles, width int) int {
	if width <= 0 {
		width = 80
	}
	return max(1, width-styles.Panel.GetHorizontalFrameSize())
}

func renderInteractionTitle(styles InteractionStyles, width int, tone InteractionTone, title, meta string) string {
	titleStyle := styles.Title
	if tone == InteractionToneSuccess {
		titleStyle = styles.Success
	}
	left := titleStyle.Render(strings.ToUpper(title))
	if meta == "" {
		return left
	}
	right := styles.Meta.Render(meta)
	spaces := width - lipgloss.Width(left) - lipgloss.Width(right)
	if spaces < 1 {
		return left + "\n" + ansi.Hardwrap(right, width, false)
	}
	return left + strings.Repeat(" ", spaces) + right
}

func renderInteractionHeadline(styles InteractionStyles, width int, tone InteractionTone, toneText, headline string) []string {
	var prefix string
	if toneText != "" {
		prefix = interactionToneStyle(styles, tone).Render(strings.ToUpper(toneText))
	}
	line := prefix
	if line != "" && headline != "" {
		line += "  "
	}
	line += styles.Body.Render(headline)
	return strings.Split(ansi.Hardwrap(line, width, false), "\n")
}

func renderInteractionRow(styles InteractionStyles, width int, row InteractionRow) []string {
	const preferredLabelWidth = 9
	labelWidth := min(preferredLabelWidth, max(1, width/3))
	valueWidth := max(1, width-labelWidth-1)
	valueLines := strings.Split(ansi.Hardwrap(row.Value, valueWidth, false), "\n")
	lines := make([]string, 0, len(valueLines))
	for i, value := range valueLines {
		label := ""
		if i == 0 {
			label = styles.Label.Render(padRight(row.Label, labelWidth))
		} else {
			label = strings.Repeat(" ", labelWidth)
		}
		lines = append(lines, label+" "+styles.Body.Render(value))
	}
	return lines
}

func packInteractionActions(styles InteractionStyles, width int, actions []InteractionAction) []string {
	var lines []string
	current := ""
	for _, action := range actions {
		part := renderInteractionAction(styles, action)
		candidate := part
		if current != "" {
			candidate = current + "  " + part
		}
		if current != "" && lipgloss.Width(candidate) > width {
			lines = append(lines, current)
			current = part
			continue
		}
		current = candidate
	}
	if current != "" {
		lines = append(lines, current)
	}
	return lines
}

func renderInteractionAction(styles InteractionStyles, action InteractionAction) string {
	if action.Selected {
		return styles.Selected.Render(strings.TrimSpace(action.Key + " " + action.Label))
	}
	key := styles.Key.Render(action.Key)
	if action.Label == "" {
		return key
	}
	return key + styles.Action.Render(action.Label)
}

func interactionToneStyle(styles InteractionStyles, tone InteractionTone) lipgloss.Style {
	switch tone {
	case InteractionToneDanger:
		return styles.Danger
	case InteractionToneWarning:
		return styles.Warning
	case InteractionToneSuccess:
		return styles.Success
	default:
		return styles.Info
	}
}

func interactionToneColor(styles InteractionStyles, tone InteractionTone) lipgloss.Color {
	switch tone {
	case InteractionToneDanger:
		return styles.Palette.Danger
	case InteractionToneWarning:
		return styles.Palette.Warning
	case InteractionToneSuccess:
		return styles.Palette.Success
	default:
		return styles.Palette.Accent
	}
}

func padRight(value string, width int) string {
	padding := width - lipgloss.Width(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}
