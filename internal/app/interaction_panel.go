package app

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
)

type interactionTone int

const (
	interactionToneInfo interactionTone = iota
	interactionToneWarning
	interactionToneDanger
)

type interactionRow struct {
	Label string
	Value string
}

type interactionAction struct {
	Key      string
	Label    string
	Selected bool
}

type interactionPanel struct {
	Title    string
	Meta     string
	Tone     interactionTone
	ToneText string
	Headline string
	Rows     []interactionRow
	Body     []string
	Choices  []interactionAction
	Actions  []interactionAction
}

func renderInteractionPanel(styles ui.InteractionStyles, width int, panel interactionPanel) string {
	if width <= 0 {
		width = 80
	}
	panelStyle := styles.Panel.Copy().BorderForeground(interactionToneColor(styles, panel.Tone))
	innerWidth := max(1, width-panelStyle.GetHorizontalFrameSize())
	lines := []string{renderInteractionTitle(styles, innerWidth, panel.Title, panel.Meta)}
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

func interactionPanelInnerWidth(styles ui.InteractionStyles, width int) int {
	if width <= 0 {
		width = 80
	}
	return max(1, width-styles.Panel.GetHorizontalFrameSize())
}

func renderInteractionTitle(styles ui.InteractionStyles, width int, title, meta string) string {
	left := styles.Title.Render(strings.ToUpper(title))
	if meta == "" {
		return left
	}
	right := styles.Meta.Render(meta)
	spaces := width - lipgloss.Width(left) - lipgloss.Width(right)
	if spaces < 1 {
		return left + "\n" + right
	}
	return left + strings.Repeat(" ", spaces) + right
}

func renderInteractionHeadline(styles ui.InteractionStyles, width int, tone interactionTone, toneText, headline string) []string {
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

func renderInteractionRow(styles ui.InteractionStyles, width int, row interactionRow) []string {
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

func packInteractionActions(styles ui.InteractionStyles, width int, actions []interactionAction) []string {
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

func renderInteractionAction(styles ui.InteractionStyles, action interactionAction) string {
	if action.Selected {
		return styles.Selected.Render(strings.TrimSpace(action.Key + " " + action.Label))
	}
	key := styles.Key.Render(action.Key)
	if action.Label == "" {
		return key
	}
	return key + styles.Action.Render(action.Label)
}

func interactionToneStyle(styles ui.InteractionStyles, tone interactionTone) lipgloss.Style {
	switch tone {
	case interactionToneDanger:
		return styles.Danger
	case interactionToneWarning:
		return styles.Warning
	default:
		return styles.Info
	}
}

func interactionToneColor(styles ui.InteractionStyles, tone interactionTone) lipgloss.Color {
	switch tone {
	case interactionToneDanger:
		return styles.Palette.Danger
	case interactionToneWarning:
		return styles.Palette.Warning
	default:
		return styles.Palette.Accent
	}
}
