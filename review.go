package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var errReviewUnavailable = errors.New("tool execution requires review but interactive approval is unavailable")

// toolReviewer manages interactive approval of tool executions (file writes,
// shell commands, etc.) before they run. It owns the review channel, approval
// state, and safety heuristics, isolating this concern from the main Mods model.
type toolReviewer struct {
	reviewChan    chan toolReviewItem
	reviewMode    ReviewMode
	reviewPending bool
	reviewItem    *toolReviewItem
	rules         approvalRuleSet
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
	debugPrintf("toolReviewer: session started, reviewChan created")
	return r.pollReviewCmd()
}

func (r *toolReviewer) handleStartMsg(msg toolReviewStartMsg) {
	r.reviewPending = true
	item := msg.item
	r.reviewItem = &item
}

func (r *toolReviewer) reset() {
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
		r.reviewItem.resp <- reviewResponse{approved: true}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "n", "N":
		r.reviewItem.resp <- reviewResponse{}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "a", "A":
		r.rules.add(r.reviewItem.alwaysRules...)
		r.reviewItem.resp <- reviewResponse{approved: true, remember: true}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "ctrl+c":
		r.reviewItem.resp <- reviewResponse{}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	}
	return true, nil
}

func (r *toolReviewer) shouldReviewTool(name string) bool {
	switch r.reviewMode {
	case ReviewNever:
		debugPrintf("Review skipped: mode is never (tool=%s)", name)
		return false
	case ReviewAlways:
		debugPrintf("Review required: mode is always (tool=%s)", name)
		return true
	default:
		mutable := isMutableTool(name)
		if mutable {
			debugPrintf("Review required: mutable tool '%s' with mode=%q", name, r.reviewMode)
		} else {
			debugPrintf("Review skipped: tool '%s' not in mutable whitelist", name)
		}
		return mutable
	}
}

func (r *toolReviewer) requestApproval(ctx *Mods, name string, data []byte) error {
	debugPrintf("requestApproval called: name=%s", name)
	if r.rules.allows(name, data) {
		debugPrintf("requestApproval: matched conversation approval rule, auto-approving")
		return nil
	}
	if r.reviewMode != ReviewAlways && name == "shell_run" {
		cmd := extractShellCommand(data)
		if cmd != "" {
			mutable := ctx.classifyShellCommand(cmd)
			debugPrintf("shell classifier: cmd=%q mutable=%v", cmd, mutable)
			if !mutable {
				debugPrintf("shell classifier result: NOT mutable, auto-approving")
				return nil
			}
		}
	}
	if !isInputTTY() {
		debugPrintf("Review denied: stdin is not a TTY (tool=%s)", name)
		return fmt.Errorf(
			"%w: %s requires approval, but stdin is not a TTY; run interactively or use --review never if non-interactive execution is intentional",
			errReviewUnavailable,
			name,
		)
	}
	respCh := make(chan reviewResponse, 1)
	alwaysRules := approvalRulesFor(name, data)
	item := toolReviewItem{
		name:        name,
		args:        data,
		alwaysRules: alwaysRules,
		resp:        respCh,
	}
	select {
	case r.reviewChan <- item:
		debugPrintf("requestApproval: review item sent to channel, waiting for user...")
	case <-ctx.ctx.Done():
		debugPrintf("requestApproval: context cancelled while sending review item")
		return fmt.Errorf("execution denied by user for: %s", name)
	}
	select {
	case response := <-respCh:
		debugPrintf("requestApproval: user response received, approved=%v remember=%v",
			response.approved, response.remember)
		if response.approved {
			return nil
		}
		return fmt.Errorf("execution denied by user for: %s", name)
	case <-ctx.ctx.Done():
		debugPrintf("requestApproval: context cancelled while waiting for user response")
		return fmt.Errorf("execution denied by user for: %s", name)
	}
}

func extractShellCommand(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
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
		fmt.Sprintf(
			"  [Y] Approve  [N] Deny  [A] Always allow: %s  [Ctrl+C] Cancel",
			approvalRulesLabel(r.reviewItem.alwaysRules),
		),
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
