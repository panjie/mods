package main

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/charmbracelet/lipgloss"
	tea "github.com/charmbracelet/bubbletea"
)

// toolReviewer manages interactive approval of tool executions (file writes,
// shell commands, etc.) before they run. It owns the review channel, approval
// state, and safety heuristics, isolating this concern from the main Mods model.
type toolReviewer struct {
	reviewChan    chan toolReviewItem
	reviewMode    ReviewMode
	approveAll    atomic.Bool
	reviewPending bool
	reviewItem    *toolReviewItem

}

func newToolReviewer(cfg *Config) *toolReviewer {
	return &toolReviewer{
		reviewMode: cfg.ReviewMode,
	}
}

func (r *toolReviewer) isPending() bool {
	return r.reviewPending && r.reviewItem != nil
}

func (r *toolReviewer) pollReviewCmd() tea.Cmd {
	return func() tea.Msg {
		item, ok := <-r.reviewChan
		if !ok {
			return nil
		}
		return toolReviewStartMsg{item: item}
	}
}

func (r *toolReviewer) startSession() tea.Cmd {
	if r.reviewChan != nil {
		close(r.reviewChan)
	}
	r.reviewChan = make(chan toolReviewItem, 4)
	return r.pollReviewCmd()
}

func (r *toolReviewer) handleStartMsg(msg toolReviewStartMsg) {
	r.reviewPending = true
	item := msg.item
	r.reviewItem = &item
}

func (r *toolReviewer) reset() {
	r.approveAll.Store(false)
	if r.reviewChan != nil {
		close(r.reviewChan)
	}
	r.reviewChan = nil
	r.reviewPending = false
	r.reviewItem = nil
}

// handleKey processes a key press during review. Returns (handled, cmd).
// When review is not pending, returns (false, nil) so the caller can handle
// the key normally.
func (r *toolReviewer) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !r.isPending() {
		return false, nil
	}
	switch msg.String() {
	case "y", "Y":
		r.reviewItem.resp <- true
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "n", "N":
		r.reviewItem.resp <- false
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "a", "A":
		r.approveAll.Store(true)
		r.reviewItem.resp <- true
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "ctrl+c":
		r.reviewItem.resp <- false
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	}
	return true, nil
}

func (r *toolReviewer) shouldReviewTool(name string) bool {
	if !isInputTTY() {
		return false
	}
	switch r.reviewMode {
	case ReviewNever:
		return false
	case ReviewAlways:
		return true
	default:
		return isMutableTool(name)
	}
}

func (r *toolReviewer) requestApproval(ctx *Mods, name string, data []byte) bool {
	if r.approveAll.Load() {
		return true
	}
	respCh := make(chan bool, 1)
	item := toolReviewItem{
		name:  name,
		args:  data,
		label: toolOperationLabel(name, data, ctx.width),
		resp:  respCh,
	}
	select {
	case r.reviewChan <- item:
	case <-ctx.ctx.Done():
		return false
	}
	select {
	case approved := <-respCh:
		return approved
	case <-ctx.ctx.Done():
		return false
	}
}

func isMutableTool(name string) bool {
	switch name {
	case "fs_write_file", "fs_apply_patch", "shell_run":
		return true
	default:
		return false
	}
}

func (r *toolReviewer) renderBanner(content string, width int, reviewPrompt, reviewChoices lipgloss.Style) string {
	if width <= 0 {
		width = 80
	}
	label := formatReviewLabel(r.reviewItem.name, r.reviewItem.args)
	promptLine := reviewPrompt.Copy().Width(width).Render(
		padRight("  Review: "+label, width-4),
	)
	choicesLine := reviewChoices.Copy().Width(width).Render(
		"  [Y] Approve  [N] Deny  [A] Approve All  [Ctrl+C] Cancel",
	)
	block := promptLine + "\n" + choicesLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func padRight(s string, w int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(runes))
}

func formatReviewLabel(name string, args []byte) string {
	parsed := toolOperationArgs(args)
	switch name {
	case "fs_write_file":
		path := oneLinePreview(argString(parsed, "path"))
		content := argString(parsed, "content")
		size := len(content)
		return fmt.Sprintf("Write %s (%d bytes)", path, size)
	case "fs_apply_patch":
		return "Apply patch to workspace files"
	case "shell_run":
		cmd := oneLinePreview(argString(parsed, "command"))
		return fmt.Sprintf("Run: %s", cmd)
	default:
		summary := toolArgsSummary(parsed)
		if summary != "" {
			return fmt.Sprintf("Execute %s (%s)", name, summary)
		}
		return fmt.Sprintf("Execute %s", name)
	}
}
