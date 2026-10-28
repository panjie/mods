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

func (r *toolReviewer) renderPagedReview(width, height int, styles ui.InteractionStyles) string {
	p := r.reviewItem.presentation
	if (width < 40 || height < 16) && r.reviewItem.name == "http_download" {
		r.reviewPages = 0
		return renderInteractionPanel(styles, width, interactionPanel{Title: "Review", Headline: "Enlarge terminal to review all content (40 columns × 16 rows).", Actions: r.reviewActions()})
	}
	inner := max(1, interactionPanelInnerWidth(styles, width))
	rows := reviewOrderedRows(p.rows)
	var lines []string
	for _, row := range rows {
		text := row.Label + ": " + reviewLiteral(row.Value)
		lines = append(lines, strings.Split(ansi.Hardwrap(text, inner, false), "\n")...)
	}
	// Reserve the actual wrapped header and controls before allocating pages.
	reservedActions := []interactionAction{{Key: "↑/↓", Label: "Pages"}}
	for _, option := range r.reviewOptions() {
		reservedActions = append(reservedActions, interactionAction{Key: option.key, Label: option.label, Selected: true})
	}
	overhead := lipgloss.Height(renderInteractionPanel(styles, width, interactionPanel{
		Title: "Review", Meta: "1/1", Tone: p.tone, ToneText: p.toneText, Headline: p.headline, Actions: reservedActions,
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
	return renderInteractionPanel(styles, width, interactionPanel{
		Title: "Review", Meta: fmt.Sprintf("%d/%d", r.reviewPage+1, r.reviewPages),
		Tone: p.tone, ToneText: p.toneText, Headline: p.headline,
		Body: body, Actions: actions,
	})
}
