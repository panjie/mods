package app

import (
	"fmt"
	"strings"
	"unicode"

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
	if !pagedReviewTool(r.reviewItem.name) {
		return true
	}
	return r.reviewPages > 0 && r.reviewSeen >= r.reviewPages-1
}

func pagedReviewTool(name string) bool {
	switch name {
	case "script_run", "http_download", "shell_run", "powershell_run", "process_run":
		return true
	}
	return false
}

func (r *toolReviewer) renderPagedReview(width, height int, styles ui.InteractionStyles) string {
	p := r.reviewItem.presentation
	if width < 40 || height < 16 {
		r.reviewPages = 0
		return renderInteractionPanel(styles, width, interactionPanel{Title: "Review required", Headline: "Enlarge terminal to review all content (40 columns × 16 rows).", Actions: []interactionAction{{Key: "N", Label: "Deny"}, {Key: "Ctrl+C", Label: "Cancel"}}})
	}
	inner := max(1, interactionPanelInnerWidth(styles, width))
	var lines []string
	rows := append([]interactionRow(nil), p.rows...)
	if len(r.reviewItem.candidateRules) > 0 {
		rows = append(rows, interactionRow{Label: "Always", Value: RulesLabel(r.reviewItem.candidateRules)})
	}
	for _, row := range rows {
		text := row.Label + ": " + reviewLiteral(row.Value)
		lines = append(lines, strings.Split(ansi.Hardwrap(text, inner, false), "\n")...)
	}
	pageSize := max(1, height-12)
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
	var actions []interactionAction
	for i, option := range r.reviewOptions() {
		if !r.canApprovePages() && (option.action == reviewOptionApprove || option.action == reviewOptionAlwaysAllow) {
			continue
		}
		actions = append(actions, interactionAction{Key: option.key, Label: option.label, Selected: i == r.selected})
	}
	actions = append(actions, interactionAction{Key: "↑/↓", Label: "Pages"})
	return renderInteractionPanel(styles, width, interactionPanel{
		Title: "Full review", Meta: fmt.Sprintf("%d/%d", r.reviewPage+1, r.reviewPages),
		Tone: p.tone, ToneText: p.toneText, Headline: p.headline,
		Body: lines[start:end], Actions: actions,
	})
}
