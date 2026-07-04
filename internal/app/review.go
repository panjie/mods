package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/pathutil"
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
		{label: "[Y] Allow once", action: reviewOptionApprove},
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

// buildAccessIntent derives the unified AccessIntent for a tool call. Shell
// tools use the dynamic analyzer (class from NeedsReview, dirs from
// AffectedDirs); tools with a registered IntentExtractor use that; read-only
// tools without directory semantics become read intents; anything else
// degrades fail-closed to a write with no known dirs.
func buildAccessIntent(name string, data []byte, registry *toolregistry.Registry, analyze func(string, string) shellCommandAnalysis) AccessIntent {
	if registry != nil && registry.ShellExecution(name) {
		if analyze == nil {
			a := defaultShellCommandAnalysis()
			return AccessIntent{Class: shellAccessMode(a)}
		}
		a := analyze(name, ExtractShellCommand(data))
		return AccessIntent{Class: shellAccessMode(a), Dirs: normalizeShellAffectedDirsForTool(a.AffectedDirs, "", name)}
	}
	if registry != nil {
		if ext, ok := registry.IntentExtractor(name); ok {
			return ext(data)
		}
		if registry.ReadOnly(name) {
			return AccessIntent{Class: AccessRead}
		}
	}
	return AccessIntent{Class: AccessWrite}
}

// shellAccessMode derives the read/write access class from a shell
// analysis: a command the LLM flags as not needing review is a read;
// anything else (mutable, unknown, or unparsed) is treated as a write.
func shellAccessMode(a shellCommandAnalysis) AccessClass {
	if a.NeedsReview {
		return AccessWrite
	}
	return AccessRead
}

// normalizeAffectedDirs reduces file paths to their parent directories and
// drops entries already covered by a broader directory, so the review
// summary and candidate DirAllow rule only mention directories. The LLM
// classifier and external-path extractor occasionally report a file path
// alongside (or instead of) its containing directory; without this, the
// "Always allows" line would advertise a file name as if it were a dir.
func normalizeAffectedDirs(dirs []string) []string {
	return normalizeAffectedDirsForWorkspace(dirs, "")
}

func normalizeAffectedDirsForWorkspace(dirs []string, workspace string) []string {
	return pathutil.NormalizeDirs(dirs, pathutil.DefaultOptions(workspace, pathutil.FlavorPOSIX))
}

func normalizeShellAffectedDirsForWorkspace(dirs []string, workspace string) []string {
	return pathutil.NormalizeShellDirs(dirs, pathutil.DefaultOptions(workspace, pathutil.FlavorPOSIX))
}

func normalizeShellAffectedDirsForTool(dirs []string, workspace string, tool string) []string {
	return pathutil.NormalizeShellDirs(dirs, pathutil.DefaultOptions(workspace, shellPathFlavor(tool)))
}

// reviewerDeps carries the three things requestApproval needs from its
// caller. Keeping them in a struct (instead of reaching into *Mods)
// lets the reviewer stay physically independent of the main model: it
// can be constructed, tested, and reasoned about without a live Mods.
//
// All fields are nil-safe; a nil func means "the answer is false" /
// "no analysis available", matching the previous behaviour where a
// missing currentToolRegistry produced shellExecution=false.
type reviewerDeps struct {
	ctx              context.Context
	isShellExecution func(name string) bool
	analyzeShell     func(tool, command string) shellCommandAnalysis
	accessIntent     AccessIntent
}

func (r *toolReviewer) requestApproval(deps reviewerDeps, name string, data []byte) error {
	debug.Printf("requestApproval called: name=%s", name)
	shellExecution := deps.isShellExecution != nil && deps.isShellExecution(name)
	intent := deps.accessIntent
	var analysis shellCommandAnalysis
	if shellExecution {
		if intent.Class != "" {
			analysis = shellCommandAnalysis{
				NeedsReview:  intent.Class == AccessWrite,
				AffectedDirs: normalizeShellAffectedDirsForTool(intent.Dirs, r.scope.Value, name),
			}
			intent.Dirs = analysis.AffectedDirs
		} else if cmd := extractShellCommand(data); cmd != "" && deps.analyzeShell != nil {
			analysis = deps.analyzeShell(name, cmd)
			analysis.AffectedDirs = normalizeShellAffectedDirsForTool(analysis.AffectedDirs, r.scope.Value, name)
			intent = AccessIntent{Class: shellAccessMode(analysis), Dirs: analysis.AffectedDirs}
		} else {
			analysis = defaultShellCommandAnalysis()
			intent = AccessIntent{Class: shellAccessMode(analysis)}
		}
		debug.Printf("shell analysis: needsReview=%v dirs=%v reason=%q", analysis.NeedsReview, analysis.AffectedDirs, analysis.Reason)
	}
	if intent.Class != "" && RulesAllowDirs(r.rules.Snapshot(), intent.Dirs, r.scope, intent.Class) {
		debug.Printf("requestApproval: matched affected dirs against saved approval rule")
		return nil
	}
	if intent.Class == "" && !shellExecution && r.rules.Allows(name, data, r.scope) {
		debug.Printf("requestApproval: matched session approval rule, auto-approving")
		return nil
	}
	if isSafeWorkArea(name, data, analysis) {
		debug.Printf("requestApproval: target is within safe workspace, auto-approving")
		return nil
	}
	if r.reviewMode != ReviewAlways && intent.Class != "" {
		if ClassifyAccess(intent, r.scope, safeDirs(), ApprovalReviewMode(r.reviewMode)) != DecisionAsk {
			debug.Printf("requestApproval: approval matrix allowed access")
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
	var candidateRules []Rule
	if intent.Class != "" {
		candidateRules = RulesForDirs(intent.Dirs, r.scope, intent.Class)
	} else {
		candidateRules = RulesFor(name, data, r.scope)
	}
	item := toolReviewItem{
		name:           name,
		args:           data,
		candidateRules: candidateRules,
		summary:        formatReviewSummaryWithIntent(name, data, analysis, r.scope, intent),
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
	case <-deps.ctx.Done():
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
	case <-deps.ctx.Done():
		debug.Printf("requestApproval: context cancelled while waiting for user response")
		return fmt.Errorf("execution denied by user for: %s", name)
	}
}

func safeDirs() []string {
	return []string{os.TempDir()}
}

func isUnderSafeDir(path string) bool {
	cleaned := pathutil.NormalizePath(path, pathutil.DefaultOptions("", pathutil.FlavorPOSIX))
	for _, safe := range safeDirs() {
		if pathutil.Contains(safe, cleaned) {
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

func (r *toolReviewer) renderBanner(width int, reviewPrompt, reviewChoices lipgloss.Style) string {
	if width <= 0 {
		width = 80
	}
	label := formatReviewLabel(r.reviewItem.name, r.reviewItem.args)
	// Bound the Review line to the terminal width with an ellipsis, symmetric
	// with the summary line below. Without this, long shell commands render
	// full-length and lipgloss's Width() hard-truncates them mid-token with no
	// "..." cue, leaving the user unable to tell the command continues.
	reviewLine := TruncateOperationStatus("  Review: "+label, width)
	promptLine := reviewPrompt.Copy().Width(width).Render(
		padRight(reviewLine, width-4),
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
	block := promptLine
	if r.reviewItem.summary != "" {
		block += "\n" + reviewChoices.Copy().Width(width).Render(TruncateOperationStatus(r.reviewItem.summary, width))
	}
	return block + "\n" + choicesLine
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
	case "fs_delete_file":
		return fmt.Sprintf("Delete file %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_delete_dir":
		return fmt.Sprintf("Delete directory %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_mkdir":
		return fmt.Sprintf("Create directory %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_copy":
		return fmt.Sprintf("Copy %s to %s", OneLinePreview(ArgString(parsed, "source_path")), OneLinePreview(ArgString(parsed, "dest_path")))
	case "fs_move":
		return fmt.Sprintf("Move %s to %s", OneLinePreview(ArgString(parsed, "source_path")), OneLinePreview(ArgString(parsed, "dest_path")))
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
