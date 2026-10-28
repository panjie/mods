package app

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/ui"
)

// Escape terminal controls rather than allowing source text to erase or
// visually replace the review. Newlines retain source line boundaries.
func reviewLiteral(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r != '\n' && (unicode.IsControl(r) || unicode.Is(unicode.Cf, r)) {
			fmt.Fprintf(&b, "\\u%04x", r)
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (r *toolReviewer) canApprovePages() bool {
	if r.reviewItem == nil {
		return false
	}
	if r.reviewItem.name != "http_download" {
		return true
	}
	return r.reviewPages > 0 && r.reviewSeen >= r.reviewPages-1
}

func pagedReviewTool(name string) bool {
	switch name {
	case "http_download", "shell_run", "powershell_run", "process_run":
		return true
	}
	return false
}

func isCommandReview(name string) bool {
	return name == "shell_run" || name == "powershell_run" || name == "process_run"
}

func (r *toolReviewer) renderPagedReview(width, height int, styles ui.InteractionStyles) string {
	if isCommandReview(r.reviewItem.name) {
		return r.renderCommandReview(width, height, styles)
	}
	p := r.reviewItem.presentation
	title := "Review"
	if (width < 40 || height < 16) && r.reviewItem.name == "http_download" {
		r.reviewPages = 0
		return renderInteractionPanel(styles, width, interactionPanel{Title: "Review", Headline: "Enlarge terminal to review all content (40 columns × 16 rows).", Actions: r.reviewActions()})
	}
	inner := max(1, interactionPanelInnerWidth(styles, width))
	rows := reviewOrderedRows(p.rows)
	var lines []string
	for _, row := range rows {
		text := row.Label + ": " + reviewLiteral(row.Value)
		if row.Label == "Command" {
			text = "\nCommand:\n" + reviewLiteral(row.Value)
		}
		lines = append(lines, strings.Split(ansi.Hardwrap(text, inner, false), "\n")...)
	}
	// Reserve the actual wrapped header and controls before allocating pages.
	reservedActions := []interactionAction{{Key: "↑/↓", Label: "Pages"}}
	for _, option := range r.reviewOptions() {
		reservedActions = append(reservedActions, interactionAction{Key: option.key, Label: option.label, Selected: true})
	}
	overhead := lipgloss.Height(renderInteractionPanel(styles, width, interactionPanel{
		Title: title, Meta: "1/1", Tone: p.tone, ToneText: p.toneText, Headline: p.headline, Actions: reservedActions,
	}))
	pageSize := max(1, height-overhead)
	const hint = "Review all pages to enable approval."
	if len(lines) > pageSize && r.reviewItem.name == "http_download" {
		// Reserve the navigation hint on every page so enabling approval never
		// shifts page boundaries.
		pageSize = max(1, pageSize-lipgloss.Height(ansi.Hardwrap(hint, inner, false)))
	}
	layout := fmt.Sprintf("%d/%d/%d", width, height, len(lines))
	if r.reviewLayout != layout {
		r.reviewPage, r.reviewSeen = 0, -1
		r.reviewLayout = layout
	}
	r.reviewPages = max(1, (len(lines)+pageSize-1)/pageSize)
	r.reviewPage = min(r.reviewPage, r.reviewPages-1)
	r.reviewSeen = max(r.reviewSeen, r.reviewPage)
	start := min(len(lines), r.reviewPage*pageSize)
	end := min(len(lines), start+pageSize)
	actions := r.reviewActions()
	body := append([]string(nil), lines[start:end]...)
	if !r.canApprovePages() {
		body = append(body, hint)
	}
	// The navigation hint is only meaningful while content actually spans
	// multiple pages; a single-page review has nothing to scroll to.
	if r.reviewPages > 1 {
		actions = append(actions, interactionAction{Key: "↑/↓", Label: "Pages"})
	}
	meta := ""
	if r.reviewPages > 1 || !isCommandReview(r.reviewItem.name) {
		meta = fmt.Sprintf("%d/%d", r.reviewPage+1, r.reviewPages)
	}
	return renderInteractionPanel(styles, width, interactionPanel{
		Title: title, Meta: meta,
		Tone: p.tone, ToneText: p.toneText, Headline: p.headline,
		Body: body, Actions: actions,
	})
}

// renderCommandReview presents a command approval as a short sheet: why the
// command runs, where it runs, the complete command, and an unambiguous choice
// menu. The command body pages independently while the choices stay pinned.
func (r *toolReviewer) renderCommandReview(width, height int, styles ui.InteractionStyles) string {
	p := r.reviewItem.presentation
	inner := max(1, interactionPanelInnerWidth(styles, width))
	body := commandReviewBody(styles, inner, reviewOrderedRows(p.rows))
	choices := r.reviewActions()
	// The choice keys are direct (Y/N/A), so the only fixed footer is the page
	// control on multi-page commands. The selection hint lives in the title
	// meta where it costs no vertical space. Recompute twice so the page
	// counter and page control participate in the height budget.
	chooseHint := "←/→ choose"
	if width < 40 {
		chooseHint = "←/→"
	}
	var navigation []interactionAction
	pages, pageSize := 0, 1
	for range 3 {
		if pages > 1 {
			navigation = []interactionAction{{Key: "↑/↓", Label: "pages"}}
		} else {
			navigation = nil
		}
		meta := chooseHint
		if pages > 1 {
			meta = fmt.Sprintf("%d/%d · %s", pages, pages, chooseHint)
		}
		overhead := lipgloss.Height(renderInteractionPanel(styles, width, interactionPanel{
			Title: "Run command?", Meta: meta, Tone: p.tone, Choices: choices, StackChoices: true, Actions: navigation,
		}))
		pageSize = max(1, height-overhead)
		next := max(1, (len(body)+pageSize-1)/pageSize)
		if next == pages {
			break
		}
		pages = next
	}
	if pages > 1 {
		navigation = []interactionAction{{Key: "↑/↓", Label: "pages"}}
	} else {
		navigation = nil
	}
	layout := fmt.Sprintf("command/%d/%d/%d", width, height, len(body))
	if r.reviewLayout != layout {
		r.reviewPage = 0
		r.reviewLayout = layout
	}
	r.reviewPages = pages
	r.reviewPage = min(r.reviewPage, pages-1)
	start := min(len(body), r.reviewPage*pageSize)
	end := min(len(body), start+pageSize)
	meta := chooseHint
	if pages > 1 {
		meta = fmt.Sprintf("%d/%d · %s", r.reviewPage+1, pages, chooseHint)
	}
	return renderInteractionPanel(styles, width, interactionPanel{
		Title: "Run command?", Meta: meta,
		Tone: p.tone, Body: body[start:end],
		Choices: choices, StackChoices: true, Actions: navigation,
	})
}

func commandReviewBody(styles ui.InteractionStyles, width int, rows []interactionRow) []string {
	var lines []string
	for _, row := range rows {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		value := reviewLiteral(row.Value)
		if row.Label == "Command" {
			lines = append(lines, styles.Label.Render("Command:"))
			lines = append(lines, commandSourceLines(styles, width, value)...)
			continue
		}
		label := row.Label + ":"
		style := styles.Body
		if row.Label == "Working dir" {
			style = styles.Code
		}
		lines = append(lines, reviewLabeledValue(styles, width, label, style, value)...)
	}
	return lines
}

// reviewLabeledValue keeps the label and the first value line together so
// scans such as "Purpose: ..." remain readable, while continuation lines align
// under the value column.
func reviewLabeledValue(styles ui.InteractionStyles, width int, label string, style lipgloss.Style, value string) []string {
	indent := lipgloss.Width(label) + 1
	available := max(1, width-indent)
	physical := strings.Split(value, "\n")
	var lines []string
	for _, part := range physical {
		wrapped := strings.Split(ansi.Wrap(part, available, ""), "\n")
		for i, line := range wrapped {
			if len(lines) == 0 && i == 0 {
				lines = append(lines, styles.Label.Render(label)+" "+style.Render(line))
				continue
			}
			lines = append(lines, strings.Repeat(" ", indent)+style.Render(line))
		}
	}
	if len(lines) == 0 {
		lines = append(lines, styles.Label.Render(label)+" ")
	}
	return lines
}

func commandSourceLines(styles ui.InteractionStyles, width int, source string) []string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	var lines []string
	for _, line := range strings.Split(source, "\n") {
		number, code, numbered := strings.Cut(line, " | ")
		if !numbered || !isReviewLineNumber(number) {
			for _, part := range wrapCommandSource(line, width, 2) {
				lines = append(lines, styles.Code.Render(part))
			}
			continue
		}
		prefixWidth := 2 + lipgloss.Width(number) + 3
		wrapped := wrapCommandSource(code, width, prefixWidth)
		for i, part := range wrapped {
			if i == 0 {
				lines = append(lines, "  "+styles.Muted.Render(number)+" "+styles.Rule.Render("│")+" "+styles.Code.Render(strings.TrimPrefix(part, strings.Repeat(" ", prefixWidth))))
				continue
			}
			lines = append(lines, styles.Code.Render(part))
		}
	}
	return lines
}

// wrapCommandSource wraps a command line and styles each physical line. Plain
// lines receive the indent as part of their rendered prefix; numbered lines
// pass their own gutter width so continuations stay under the code column.
func wrapCommandSource(code string, width, indent int) []string {
	available := max(1, width-indent)
	wrapped := strings.Split(ansi.Wrap(code, available, ""), "\n")
	lines := make([]string, 0, len(wrapped))
	for _, part := range wrapped {
		lines = append(lines, strings.Repeat(" ", indent)+part)
	}
	return lines
}

func isReviewLineNumber(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
