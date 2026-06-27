package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	toolregistry "github.com/panjie/mods/internal/tools"
)

var errReviewUnavailable = errors.New("tool execution requires review but interactive approval is unavailable")

type reviewOptionAction int

const (
	reviewOptionApprove reviewOptionAction = iota
	reviewOptionDeny
	reviewOptionAlwaysAllow
	reviewOptionCancel
)

type reviewOption struct {
	label  string
	action reviewOptionAction
}

// toolReviewer manages interactive approval of tool executions (file writes,
// shell commands, etc.) before they run. It owns the review channel, approval
// state, and safety heuristics, isolating this concern from the main Mods model.
//
// Concurrency model:
//
//   - reviewChan is accessed from three goroutines: Update (startSession /
//     reset replace it), pollReviewCmd's tea.Cmd goroutine (reads), and the
//     tool-caller goroutine via requestApproval (writes). The mu mutex
//     guards the field load/store. Channel ops themselves are safe; callers
//     snapshot the channel pointer under the lock and then send/receive on
//     the snapshot so a concurrent reset does not race the receive.
//
//   - reviewMode, reviewPending, reviewItem, selected, rules.scope are only
//     touched from the Update goroutine (handleStartMsg, handleKey, reset,
//     isPending, renderBanner) plus the View pass that Bubble Tea runs
//     synchronously after each Update. rules has its own internal mutex.
type toolReviewer struct {
	mu            sync.Mutex
	reviewChan    chan toolReviewItem
	reviewMode    ReviewMode
	reviewPending bool
	reviewItem    *toolReviewItem
	rules         RuleSet
	scope         Scope
	selected      int
}

func newToolReviewer(cfg *Config) *toolReviewer {
	workspace := cfg.ResolveWorkspace()
	return &toolReviewer{
		reviewMode: cfg.ReviewMode,
		scope:      WorkspaceScope(workspace.Canonical),
	}
}

func (r *toolReviewer) isPending() bool {
	return r.reviewPending && r.reviewItem != nil && r.reviewItem.resp != nil
}

// snapshotChan returns the current review channel under the mutex so callers
// in tea.Cmd / tool-caller goroutines can operate on a stable reference even
// if Update replaces the channel via startSession or reset.
func (r *toolReviewer) snapshotChan() chan toolReviewItem {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reviewChan
}

func (r *toolReviewer) pollReviewCmd() tea.Cmd {
	ch := r.snapshotChan()
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		item, ok := <-ch
		if !ok {
			return nil
		}
		return toolReviewStartMsg{item: item}
	}
}

func (r *toolReviewer) startSession() tea.Cmd {
	r.mu.Lock()
	if r.reviewChan != nil {
		close(r.reviewChan)
	}
	r.reviewChan = make(chan toolReviewItem, 4)
	ch := r.reviewChan
	r.mu.Unlock()
	debug.Printf("toolReviewer: session started, reviewChan created")
	// Capture ch (not r.reviewChan) so a subsequent reset/startSession that
	// replaces the field cannot leave this goroutine receiving from a
	// closed channel without warning.
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return toolReviewStartMsg{item: item}
	}
}

func (r *toolReviewer) handleStartMsg(msg toolReviewStartMsg) {
	r.reviewPending = true
	item := msg.item
	r.reviewItem = &item
	r.selected = 0
}

func (r *toolReviewer) reset() {
	r.mu.Lock()
	if r.reviewChan != nil {
		close(r.reviewChan)
	}
	r.reviewChan = nil
	r.mu.Unlock()
	r.reviewPending = false
	r.reviewItem = nil
	r.selected = 0
}

// handleKey processes a key press during review. Returns (handled, cmd).
// When review is not pending, returns (false, nil) so the caller can handle
// the key normally.
func (r *toolReviewer) handleKey(msg tea.KeyMsg) (bool, tea.Cmd) {
	if !r.isPending() {
		return false, nil
	}
	options := r.reviewOptions()
	if r.selected >= len(options) {
		r.selected = 0
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
		if len(r.reviewItem.candidateRules) == 0 {
			return true, nil
		}
		r.rules.Add(r.reviewItem.candidateRules...)
		r.reviewItem.resp <- reviewResponse{approved: true}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "ctrl+c":
		r.reviewItem.resp <- reviewResponse{}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "left":
		r.selected--
		if r.selected < 0 {
			r.selected = len(options) - 1
		}
		return true, nil
	case "right":
		r.selected++
		if r.selected >= len(options) {
			r.selected = 0
		}
		return true, nil
	case "enter":
		switch options[r.selected].action {
		case reviewOptionApprove:
			r.reviewItem.resp <- reviewResponse{approved: true}
		case reviewOptionDeny:
			r.reviewItem.resp <- reviewResponse{}
		case reviewOptionAlwaysAllow:
			r.rules.Add(r.reviewItem.candidateRules...)
			r.reviewItem.resp <- reviewResponse{approved: true}
		case reviewOptionCancel:
			r.reviewItem.resp <- reviewResponse{}
		}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	}
	return true, nil
}

func (r *toolReviewer) reviewOptions() []reviewOption {
	options := []reviewOption{
		{label: "[Y] Approve", action: reviewOptionApprove},
		{label: "[N] Deny", action: reviewOptionDeny},
	}
	if r.reviewItem != nil && len(r.reviewItem.candidateRules) > 0 {
		options = append(options, reviewOption{label: "[A] Always allow", action: reviewOptionAlwaysAllow})
	}
	return append(options, reviewOption{label: "[Ctrl+C] Cancel", action: reviewOptionCancel})
}

func (r *toolReviewer) shouldReviewTool(registry *toolregistry.Registry, name string) bool {
	switch r.reviewMode {
	case ReviewNever:
		debug.Printf("Review skipped: mode is never (tool=%s)", name)
		return false
	case ReviewAlways:
		debug.Printf("Review required: mode is always (tool=%s)", name)
		return true
	default:
		mutable := false
		if registry != nil {
			mutable = registry.Mutable(name)
		}
		if mutable {
			debug.Printf("Review required: mutable tool '%s' with mode=%q", name, r.reviewMode)
		} else {
			debug.Printf("Review skipped: tool '%s' not in mutable whitelist", name)
		}
		return mutable
	}
}

func (r *toolReviewer) requestApproval(ctx *Mods, name string, data []byte) error {
	debug.Printf("requestApproval called: name=%s", name)
	shellExecution := ctx.currentToolRegistry != nil && ctx.currentToolRegistry.ShellExecution(name)
	var analysis shellCommandAnalysis
	if shellExecution {
		if cmd := extractShellCommand(data); cmd != "" {
			analysis = ctx.analyzeShellCommand(name, cmd)
			debug.Printf("shell analysis: cmd=%q needsReview=%v dirs=%v reason=%q", cmd, analysis.NeedsReview, analysis.AffectedDirs, analysis.Reason)
			if RulesAllowDirs(r.rules.Snapshot(), analysis.AffectedDirs, r.scope) {
				debug.Printf("requestApproval: matched LLM affected dirs against saved approval rule")
				return nil
			}
		} else {
			analysis = defaultShellCommandAnalysis()
		}
	}
	if !shellExecution && r.rules.Allows(name, data, r.scope) {
		debug.Printf("requestApproval: matched conversation approval rule, auto-approving")
		return nil
	}
	if isSafeWorkArea(name, data, analysis) {
		debug.Printf("requestApproval: target is within safe workspace, auto-approving")
		return nil
	}
	if r.reviewMode != ReviewAlways && shellExecution {
		if !analysis.NeedsReview {
			debug.Printf("shell classifier result: NOT mutable, auto-approving")
			return nil
		}
	}
	if !IsInputTTY() {
		debug.Printf("Review denied: stdin is not a TTY (tool=%s)", name)
		return fmt.Errorf(
			"%w: %s requires approval, but stdin is not a TTY; run interactively or use --review never if non-interactive execution is intentional",
			errReviewUnavailable,
			name,
		)
	}
	respCh := make(chan reviewResponse, 1)
	candidateRules := RulesFor(name, data, r.scope)
	if shellExecution {
		candidateRules = RulesForDirs(analysis.AffectedDirs, r.scope)
	}
	item := toolReviewItem{
		name:           name,
		args:           data,
		candidateRules: candidateRules,
		resp:           respCh,
	}
	// Snapshot the channel under the lock so a concurrent reset() that
	// replaces r.reviewChan does not leave this send dispatching to a stale
	// reference; a closed-channel panic here is impossible because reset()
	// holds the lock while closing and assigning nil.
	ch := r.snapshotChan()
	if ch == nil {
		debug.Printf("requestApproval: no review channel registered (session ended)")
		return fmt.Errorf("%w: %s", errReviewUnavailable, name)
	}
	select {
	case ch <- item:
		debug.Printf("requestApproval: review item sent to channel, waiting for user...")
	case <-ctx.ctx.Done():
		debug.Printf("requestApproval: context cancelled while sending review item")
		return fmt.Errorf("execution denied by user for: %s", name)
	}
	select {
	case response := <-respCh:
		debug.Printf("requestApproval: user response received, approved=%v", response.approved)
		if response.approved {
			return nil
		}
		return fmt.Errorf("execution denied by user for: %s", name)
	case <-ctx.ctx.Done():
		debug.Printf("requestApproval: context cancelled while waiting for user response")
		return fmt.Errorf("execution denied by user for: %s", name)
	}
}

func safeDirs() []string {
	return []string{os.TempDir()}
}

func isUnderSafeDir(path string) bool {
	cleaned := filepath.Clean(path)
	for _, safe := range safeDirs() {
		safeClean := filepath.Clean(safe)
		if cleaned == safeClean || strings.HasPrefix(cleaned, safeClean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// isSafeWorkArea reports whether a tool invocation may be auto-approved
// because its real targets all live under a configured safe workspace
// directory (typically the OS temp dir).
//
// Decision is intentionally fail-closed: when the shell classifier cannot
// determine the affected directories (LLM timeout, empty result, parse
// failure -> defaultShellCommandAnalysis), we return false so the request
// falls through to the normal review flow. Previous versions retried with
// a strings.Contains(command, safeDir) fallback, which let any command
// auto-approve as long as it mentioned the safe dir name anywhere in its
// text (including inside echo arguments, comments, or URLs).
func isSafeWorkArea(name string, data []byte, analysis shellCommandAnalysis) bool {
	switch name {
	case "shell_run", "powershell_run":
		if len(analysis.AffectedDirs) == 0 {
			// No reliable signal about what the command will touch ->
			// require explicit review rather than guessing.
			return false
		}
		for _, d := range analysis.AffectedDirs {
			if !isUnderSafeDir(d) {
				return false
			}
		}
		return true
	case "fs_write_file":
		var parsed struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil || parsed.Path == "" {
			return false
		}
		return isUnderSafeDir(parsed.Path)
	default:
		return false
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

func (r *toolReviewer) renderBanner(content string, width int, reviewPrompt, reviewChoices lipgloss.Style) string {
	if width <= 0 {
		width = 80
	}
	label := formatReviewLabel(r.reviewItem.name, r.reviewItem.args)
	promptLine := reviewPrompt.Copy().Width(width).Render(
		padRight("  Review: "+label, width-4),
	)
	baseStyle := reviewChoices.Copy().Padding(0, 0)
	selectedStyle := baseStyle.Copy().
		Foreground(lipgloss.Color("#4A3B9F")).
		Background(lipgloss.Color("#E0DDFF"))
	options := r.reviewOptions()
	selected := r.selected
	if selected >= len(options) {
		selected = 0
	}
	var parts []string
	for i, opt := range options {
		if i == selected {
			parts = append(parts, selectedStyle.Render(opt.label))
		} else {
			parts = append(parts, baseStyle.Render(opt.label))
		}
	}
	separator := baseStyle.Render("  ")
	choicesLine := reviewChoices.Copy().Width(width).Render(strings.Join(parts, separator))
	alwaysLine := reviewChoices.Copy().Width(width).Render(formatAlwaysAllowSummary(r.reviewItem.candidateRules, r.scope, width))
	block := promptLine + "\n" + choicesLine + "\n" + alwaysLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func formatAlwaysAllowSummary(rules []Rule, _ Scope, width int) string {
	if len(rules) == 0 {
		return TruncateOperationStatus("No reusable allow rule for this command.", width)
	}
	dirs := alwaysAllowDirs(rules)
	if len(dirs) == 1 {
		return TruncateOperationStatus("Always allows writes in "+dirs[0], width)
	}
	if len(dirs) > 1 {
		return TruncateOperationStatus("Always allows writes in: "+strings.Join(dirs, ", "), width)
	}
	return TruncateOperationStatus("Always allows: "+RulesLabel(rules), width)
}

func alwaysAllowDirs(rules []Rule) []string {
	var dirs []string
	for _, rule := range rules {
		dirs = append(dirs, rule.Paths...)
	}
	return dirs
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
	parsed := ToolOperationArgs(args)
	switch name {
	case "fs_write_file":
		path := OneLinePreview(ArgString(parsed, "path"))
		content := ArgString(parsed, "content")
		size := len(content)
		return fmt.Sprintf("Write %s (%d bytes)", path, size)
	case "fs_apply_patch":
		return "Apply patch to workspace files"
	case "shell_run":
		cmd := ShellCommandPreview(ArgString(parsed, "command"))
		return fmt.Sprintf("Run: %s", cmd)
	case "powershell_run":
		cmd := ShellCommandPreview(ArgString(parsed, "command"))
		return fmt.Sprintf("Run PowerShell: %s", cmd)
	default:
		summary := ToolArgsSummary(parsed)
		if summary != "" {
			return fmt.Sprintf("Execute %s (%s)", name, summary)
		}
		return fmt.Sprintf("Execute %s", name)
	}
}
