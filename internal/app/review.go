package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/pathutil"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
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
	key    string
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
	mu                         sync.Mutex
	reviewChan                 chan toolReviewItem
	reviewMode                 ReviewMode
	reviewPending              bool
	reviewItem                 *toolReviewItem
	rules                      RuleSet
	scope                      Scope
	selected                   int
	raw                        bool
	reviewAvailabilityKnown    bool
	interactiveReviewAvailable bool
}

func newToolReviewer(cfg *Config) *toolReviewer {
	workspace := cfg.ResolveWorkspace()
	return &toolReviewer{
		reviewMode:                 cfg.ReviewMode,
		scope:                      WorkspaceScope(workspace.Canonical),
		raw:                        cfg.Raw,
		reviewAvailabilityKnown:    true,
		interactiveReviewAvailable: !cfg.Raw && cfg.InteractiveReviewAvailable,
	}
}

func (r *toolReviewer) canReviewInteractively() bool {
	if r.raw {
		return false
	}
	if r.reviewAvailabilityKnown {
		return r.interactiveReviewAvailable
	}
	return IsInputTTY()
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
		{key: "Y", label: "Allow once", action: reviewOptionApprove},
		{key: "N", label: "Deny", action: reviewOptionDeny},
	}
	if r.reviewItem != nil && len(r.reviewItem.candidateRules) > 0 {
		options = append(options, reviewOption{key: "A", label: "Always allow", action: reviewOptionAlwaysAllow})
	}
	return append(options, reviewOption{key: "Ctrl+C", label: "Cancel", action: reviewOptionCancel})
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
		return AccessIntent{Class: shellAccessMode(a), Dirs: normalizeShellAffectedDirsForTool(a.AffectedDirs, "", name), Reason: a.Reason}
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
	if a.Effect == shellEffectUnknown || a.Effect == shellEffectWrite {
		return AccessWrite
	}
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

func normalizeAccessIntentDirs(intent AccessIntent, workspace, tool string, shell bool) AccessIntent {
	normalize := func(dirs []string) []string {
		if shell {
			return normalizeShellAffectedDirsForTool(dirs, workspace, tool)
		}
		return normalizeAffectedDirsForWorkspace(dirs, workspace)
	}
	if intent.ReadDirs != nil || intent.WriteDirs != nil {
		if intent.ReadDirs != nil {
			intent.ReadDirs = normalize(intent.ReadDirs)
		}
		if intent.WriteDirs != nil {
			intent.WriteDirs = normalize(intent.WriteDirs)
		}
		return intent
	}
	if len(intent.Dirs) > 0 {
		intent.Dirs = normalize(intent.Dirs)
	}
	return intent
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
		cmd := extractShellCommand(data)
		analyzed := cmd != "" && deps.analyzeShell != nil
		if analyzed {
			analysis = deps.analyzeShell(name, cmd)
			analysis.AffectedDirs = normalizeShellAffectedDirsForTool(analysis.AffectedDirs, r.scope.Value, name)
		}
		if intent.HasAccess() {
			class := intent.DominantClass()
			dirs := normalizeShellAffectedDirsForTool(intent.AllDirs(), r.scope.Value, name)
			if analysis.Effect == "" {
				reason := intent.Reason
				if reason == "" {
					reason = analysis.Reason
				}
				analysis = shellCommandAnalysis{
					NeedsReview:  class == AccessWrite,
					AffectedDirs: dirs,
					Reason:       reason,
				}
				analysis = normalizeShellEffect(analysis)
			} else if intent.Reason != "" && (analysis.Reason == "" || analysis.Reason == defaultShellCommandAnalysis().Reason) {
				analysis.Reason = intent.Reason
			}
			intent = AccessIntent{Class: class, Dirs: dirs, Reason: intent.Reason}
		} else if analyzed || analysis.Effect != "" {
			intent = AccessIntent{Class: shellAccessMode(analysis), Dirs: analysis.AffectedDirs, Reason: analysis.Reason}
		} else {
			analysis = defaultShellCommandAnalysis()
			intent = AccessIntent{Class: shellAccessMode(analysis), Reason: analysis.Reason}
		}
		debug.Printf("shell analysis: cmd=%q needsReview=%v dirs=%v reason=%q", debug.Truncate(cmd, 500), analysis.NeedsReview, analysis.AffectedDirs, analysis.Reason)
	}
	if intent.HasAccess() && RulesAllowIntent(r.rules.Snapshot(), intent, r.scope, safeDirs(), ApprovalReviewMode(r.reviewMode)) {
		debug.Printf("requestApproval: matched affected dirs against saved approval rule")
		return nil
	}
	if r.reviewMode != ReviewAlways && intent.HasAccess() {
		if ClassifyAccess(intent, r.scope, safeDirs(), ApprovalReviewMode(r.reviewMode)) != DecisionAsk {
			debug.Printf("requestApproval: approval matrix allowed access")
			return nil
		}
	}
	if !r.canReviewInteractively() {
		debug.Printf("Review denied: no interactive approval channel (tool=%s)", name)
		return fmt.Errorf(
			"%w: %s requires approval, but no interactive approval channel is available; run interactively or use --review-mode never if non-interactive execution is intentional",
			errReviewUnavailable,
			name,
		)
	}
	respCh := make(chan reviewResponse, 1)
	var candidateRules []Rule
	if intent.HasAccess() {
		candidateRules = candidateRulesForIntent(intent, r.scope, safeDirs(), ApprovalReviewMode(r.reviewMode), shellExecution)
	}
	presentationAnalysis := analysis
	item := toolReviewItem{
		name:           name,
		args:           data,
		candidateRules: candidateRules,
		summary:        formatReviewSummaryWithIntent(name, data, presentationAnalysis, r.scope, intent),
		presentation:   formatReviewPresentationWithIntent(name, data, presentationAnalysis, r.scope, intent),
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

// requestSecretApproval is an unpersistable, per-use confirmation. Secret
// references are capabilities: saved directory/tool rules and ReviewNever do
// not authorize sending one to an external process or MCP server.
func (r *toolReviewer) requestSecretApproval(ctx context.Context, name string, data []byte) error {
	if !r.canReviewInteractively() {
		return fmt.Errorf("%w: using a protected credential requires an interactive terminal", errReviewUnavailable)
	}
	respCh := make(chan reviewResponse, 1)
	item := toolReviewItem{
		name:    name,
		args:    data,
		summary: fmt.Sprintf("Use protected credential with %s\n\nArguments: %s", name, string(data)),
		presentation: reviewPresentation{
			tone:     interactionToneDanger,
			toneText: "Danger",
			headline: "Send a protected credential to an external tool",
			rows: []interactionRow{
				{Label: "Tool", Value: name},
				{Label: "Target", Value: secretReferenceTargets(data)},
			},
		},
		resp: respCh,
	}
	ch := r.snapshotChan()
	if ch == nil {
		return fmt.Errorf("%w: %s", errReviewUnavailable, name)
	}
	select {
	case ch <- item:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case response := <-respCh:
		if response.approved {
			return nil
		}
		return fmt.Errorf("protected credential use denied by user for: %s", name)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func candidateRulesForIntent(intent AccessIntent, scope Scope, safeDirs []string, reviewMode ApprovalReviewMode, shell bool) []Rule {
	var rules []Rule
	for _, group := range intent.Groups() {
		groupIntent := AccessIntent{Class: group.Class, Dirs: group.Dirs}
		if reviewMode != ApprovalReviewMode(ReviewAlways) && ClassifyAccess(groupIntent, scope, safeDirs, reviewMode) != DecisionAsk {
			continue
		}
		dirs := group.Dirs
		if shell && group.Class == AccessRead {
			dirs = ExternalDirs(groupIntent, scope, safeDirs)
		}
		rules = append(rules, RulesForDirs(dirs, scope, group.Class)...)
	}
	return rules
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

func (r *toolReviewer) renderBanner(width int, styles ui.InteractionStyles) string {
	if width <= 0 {
		width = 80
	}
	options := r.reviewOptions()
	selected := r.selected
	if selected >= len(options) {
		selected = 0
	}
	actions := make([]interactionAction, 0, len(options))
	for i, opt := range options {
		actions = append(actions, interactionAction{Key: opt.key, Label: opt.label, Selected: i == selected})
	}
	presentation := r.reviewItem.presentation
	if presentation.headline == "" && r.reviewItem.summary != "" {
		presentation = reviewPresentation{
			tone: interactionToneWarning, toneText: "Warning", headline: r.reviewItem.summary,
		}
	}
	rows := append([]interactionRow(nil), presentation.rows...)
	if len(r.reviewItem.candidateRules) > 0 {
		rows = append(rows, interactionRow{Label: "Always", Value: RulesLabel(r.reviewItem.candidateRules)})
	}
	return renderInteractionPanel(styles, width, interactionPanel{
		Title:    "Review required",
		Meta:     r.reviewItem.name,
		Tone:     presentation.tone,
		ToneText: presentation.toneText,
		Headline: presentation.headline,
		Rows:     rows,
		Actions:  actions,
	})
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
	case "fs_read_file":
		return fmt.Sprintf("Read %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_list_dir":
		return fmt.Sprintf("List %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_stat":
		return fmt.Sprintf("Stat %s", OneLinePreview(ArgString(parsed, "path")))
	case "fs_search":
		return fmt.Sprintf("Search %q in %s", ArgString(parsed, "query"), OneLinePreview(ArgString(parsed, "path")))
	case "fs_largest":
		return fmt.Sprintf("Largest in %s", OneLinePreview(ArgString(parsed, "path")))
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
