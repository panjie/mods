package ui

import (
	"image/color"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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

// InteractionGroup renders a titled set of related rows. Use it for summaries
// and other reference panels where label/value columns should align.
type InteractionGroup struct {
	Title string
	Rows  []InteractionRow
}

// InteractionProgress renders a slim, full-width progress bar. Current is
// 1-based and clamped to Total.
type InteractionProgress struct {
	Current int
	Total   int
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
	// Progress draws a thin progress bar below the title.
	Progress InteractionProgress
	Rows     []InteractionRow
	Body     []string
	Choices  []InteractionAction
	// StackChoices renders one choice per line instead of packing choices
	// horizontally. This is useful when choices are navigated with up/down.
	StackChoices bool
	Actions      []InteractionAction
	// Cursor is relative to Body[CursorBody]. It is ignored when nil.
	Cursor     *tea.Cursor
	CursorBody int
}

func RenderInteractionPanel(styles InteractionStyles, width int, panel InteractionPanel) string {
	return RenderInteractionPanelView(styles, width, panel).Content
}

func RenderInteractionPanelView(styles InteractionStyles, width int, panel InteractionPanel) CursorView {
	if width <= 0 {
		width = 80
	}
	panelStyle := styles.Panel.BorderForeground(interactionToneColor(styles, panel.Tone))
	innerWidth := max(1, width-panelStyle.GetHorizontalFrameSize())
	lines := []string{renderInteractionTitle(styles, innerWidth, panel.Tone, panel.Title, panel.Meta)}
	if panel.Progress.Total > 0 {
		lines = append(lines, renderInteractionProgress(styles, innerWidth, panel.Progress))
	}
	var cursor *tea.Cursor
	if panel.Headline != "" || panel.ToneText != "" {
		lines = append(lines, renderInteractionHeadline(styles, innerWidth, panel.Tone, panel.ToneText, panel.Headline)...)
	}
	for _, row := range panel.Rows {
		lines = append(lines, renderInteractionRow(styles, innerWidth, row)...)
	}
	for i, body := range panel.Body {
		if cursor == nil && panel.Cursor != nil && i == panel.CursorBody {
			cursor = cloneCursor(panel.Cursor)
			cursor.Y += len(lines)
		}
		if body == "" {
			lines = append(lines, "")
			continue
		}
		lines = append(lines, ansi.Hardwrap(body, innerWidth, false))
	}
	if len(panel.Choices) > 0 {
		lines = append(lines, "")
		if panel.StackChoices {
			for _, choice := range panel.Choices {
				lines = append(lines, renderStackedInteractionAction(styles, innerWidth, choice)...)
			}
		} else {
			lines = append(lines, packInteractionActions(styles, innerWidth, panel.Choices)...)
		}
	}
	if len(panel.Actions) > 0 {
		lines = append(lines, "")
		lines = append(lines, packInteractionActions(styles, innerWidth, panel.Actions)...)
	}
	return NewCursorView(strings.Join(lines, "\n"), cursor).InStyle(panelStyle)
}

func InteractionPanelInnerWidth(styles InteractionStyles, width int) int {
	if width <= 0 {
		width = 80
	}
	return max(1, width-styles.Panel.GetHorizontalFrameSize())
}

// RenderInteractionGroups renders grouped rows as standalone lines. Callers
// use it when grouped content must participate in their own scrolling layout
// instead of being pinned below a panel body.
func RenderInteractionGroups(styles InteractionStyles, width int, groups []InteractionGroup) []string {
	labelWidth := interactionGroupLabelWidth(groups, width)
	var lines []string
	for i, group := range groups {
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, styles.Section.Render(group.Title))
		for _, row := range group.Rows {
			lines = append(lines, renderInteractionRowWidth(styles, width, row, labelWidth)...)
		}
	}
	return lines
}

// interactionGroupLabelWidth sizes one label column for every group so values
// line up across the whole summary, not just within a section. It never
// starves the value column on narrow terminals.
func interactionGroupLabelWidth(groups []InteractionGroup, width int) int {
	labelWidth := 0
	for _, group := range groups {
		for _, row := range group.Rows {
			labelWidth = max(labelWidth, lipgloss.Width(row.Label))
		}
	}
	labelWidth = max(labelWidth, 12)
	return min(labelWidth, max(1, width-8))
}

func renderInteractionTitle(styles InteractionStyles, width int, tone InteractionTone, title, meta string) string {
	titleStyle := styles.Title
	if tone == InteractionToneSuccess {
		titleStyle = styles.Success
	}
	left := titleStyle.Render(title)
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

// renderInteractionProgress keeps the bar exactly the inner width so the
// panel's right edge stays aligned with the title and footer.
func renderInteractionProgress(styles InteractionStyles, width int, progress InteractionProgress) string {
	if progress.Total <= 0 || width <= 0 {
		return ""
	}
	current := min(max(1, progress.Current), progress.Total)
	filled := (width*current + progress.Total/2) / progress.Total
	filled = min(max(0, filled), width)
	bar := styles.Info.Render(strings.Repeat("━", filled))
	if filled < width {
		bar += styles.Rule.Render(strings.Repeat("─", width-filled))
	}
	return bar
}

func renderInteractionHeadline(styles InteractionStyles, width int, tone InteractionTone, toneText, headline string) []string {
	var prefix string
	if toneText != "" {
		prefix = interactionToneStyle(styles, tone).Render(toneText)
	}
	line := prefix
	if line != "" && headline != "" {
		line += "  "
	}
	line += styles.Body.Render(headline)
	return strings.Split(ansi.Hardwrap(line, width, false), "\n")
}

func renderInteractionRow(styles InteractionStyles, width int, row InteractionRow) []string {
	// Twelve cells fit the labels used by approval panels ("Working dir" is the
	// longest) so values align without truncating a security-relevant label.
	const preferredLabelWidth = 12
	labelWidth := preferredLabelWidth
	if width < 30 {
		labelWidth = min(preferredLabelWidth, max(1, width/3))
	}
	return renderInteractionRowWidth(styles, width, row, labelWidth)
}

func renderInteractionRowWidth(styles InteractionStyles, width int, row InteractionRow, labelWidth int) []string {
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
	const separator = "  · "
	for _, action := range actions {
		part := renderInteractionAction(styles, action)
		candidate := part
		if current != "" {
			candidate = current + styles.Muted.Render(separator) + part
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
		return styles.Cursor.Render("❯") + " " + styles.Selected.Padding(0).Render(strings.TrimSpace(action.Key+" "+action.Label))
	}
	key := styles.Key.Render(action.Key)
	if action.Label == "" {
		return key
	}
	return key + styles.Action.Render(action.Label)
}

// renderStackedInteractionAction renders one stacked choice at the given width,
// hardwrapping the label so long option text never overflows the panel.
// Continuation lines are indented to align under the label start, matching the
// first-line offset produced by the styled key (or the selected highlight).
func renderStackedInteractionAction(styles InteractionStyles, width int, action InteractionAction) []string {
	keyWidth := lipgloss.Width(styles.Key.Render(action.Key))
	// Keep a cursor column so selection does not depend on color alone.
	cursor := " "
	if action.Selected {
		cursor = styles.Cursor.Render("❯")
	}
	cursorWidth := lipgloss.Width(cursor)
	labelWidth := max(1, width-cursorWidth-keyWidth-1)
	labelLines := strings.Split(ansi.Hardwrap(action.Label, labelWidth, false), "\n")
	lines := make([]string, 0, len(labelLines))
	if action.Selected {
		for i, label := range labelLines {
			if i == 0 {
				lines = append(lines, cursor+styles.Selected.Render(strings.TrimSpace(action.Key+" "+label)))
			} else {
				lines = append(lines, strings.Repeat(" ", cursorWidth)+styles.Selected.Render(strings.Repeat(" ", max(0, keyWidth-1))+label))
			}
		}
		return lines
	}
	indent := strings.Repeat(" ", cursorWidth+keyWidth)
	for i, label := range labelLines {
		if i == 0 {
			lines = append(lines, cursor+styles.Key.Render(action.Key)+styles.Action.Render(label))
		} else {
			lines = append(lines, indent+styles.Action.Render(label))
		}
	}
	return lines
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

func interactionToneColor(styles InteractionStyles, tone InteractionTone) color.Color {
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
