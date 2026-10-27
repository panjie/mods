package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/pathutil"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

var errReviewUnavailable = errors.New("tool execution requires review but interactive approval is unavailable")

type reviewOptionAction int

const (
	reviewOptionApprove reviewOptionAction = iota
	reviewOptionDeny
	reviewOptionSimplify
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
//   - reviewMode, reviewPending, reviewItem, selected, and scope are only
//     touched from the Update goroutine (handleStartMsg, handleKey, reset,
//     isPending, renderBanner) plus the View pass that Bubble Tea runs
//     synchronously after each Update. rules has its own internal mutex.
type toolReviewer struct {
	mu                         sync.Mutex
	reviewChan                 chan toolReviewItem
	reviewSessionDone          chan struct{}
	reviewMode                 ReviewMode
	reviewPending              bool
	cancelRequested            bool
	reviewItem                 *toolReviewItem
	rules                      RuleSet
	scope                      Scope
	selected                   int
	reviewPage                 int
	reviewSeen                 int
	reviewPages                int
	reviewLayout               string
	raw                        bool
	reviewAvailabilityKnown    bool
	interactiveReviewAvailable bool
}

func newToolReviewer(cfg *Config) *toolReviewer {
	cwd := cfg.ResolveWorkingDir()
	return &toolReviewer{
		reviewMode:                 cfg.ReviewMode,
		scope:                      WorkingDirScope(cwd.Canonical),
		raw:                        cfg.Raw,
		reviewAvailabilityKnown:    true,
		interactiveReviewAvailable: !cfg.Raw && cfg.InteractiveTTYAvailable,
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

// requiresHumanReview mirrors the policy gates in requestApproval without
// starting an interaction. Organization feedback may only preempt calls that
// would otherwise reach a human approval decision.
func (r *toolReviewer) requiresHumanReview(intent AccessIntent, safeDirSet []string) bool {
	return r.reviewBypassSource(intent, safeDirSet) == ""
}

func (r *toolReviewer) reviewBypassSource(intent AccessIntent, safeDirSet []string) string {
	mode := ApprovalReviewMode(r.reviewMode)
	if ClassifyAccess(intent, r.scope, safeDirSet, mode) != DecisionAsk {
		return "auto"
	}
	if r.reviewMode == ReviewAuto && intent.HasAccess() &&
		RulesAllowIntent(r.rules.Snapshot(), intent, r.scope, safeDirSet, mode) {
		return "saved rule"
	}
	return ""
}

func (r *toolReviewer) isPending() bool {
	return r.reviewPending && r.reviewItem != nil && r.reviewItem.resp != nil
}

// snapshotSession returns the current review channel and its lifetime signal
// atomically. The request channel itself is never closed: senders may already
// hold a snapshot when Update ends the session, so closing it would create a
// send-on-closed-channel race. Closing done wakes both senders and receivers.
func (r *toolReviewer) snapshotSession() (chan toolReviewItem, <-chan struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reviewChan, r.reviewSessionDone
}

func (r *toolReviewer) stopSessionLocked() {
	if r.reviewSessionDone != nil {
		close(r.reviewSessionDone)
	}
	r.reviewChan = nil
	r.reviewSessionDone = nil
}

func (r *toolReviewer) pollReviewCmd() tea.Cmd {
	ch, done := r.snapshotSession()
	return func() tea.Msg {
		if ch == nil {
			return nil
		}
		select {
		case item := <-ch:
			return toolReviewStartMsg{item: item}
		case <-done:
			return nil
		}
	}
}

func (r *toolReviewer) startSession() tea.Cmd {
	r.mu.Lock()
	r.stopSessionLocked()
	r.reviewChan = make(chan toolReviewItem, 4)
	r.reviewSessionDone = make(chan struct{})
	ch := r.reviewChan
	done := r.reviewSessionDone
	r.mu.Unlock()
	return func() tea.Msg {
		select {
		case item := <-ch:
			return toolReviewStartMsg{item: item}
		case <-done:
			return nil
		}
	}
}

func (r *toolReviewer) handleStartMsg(msg toolReviewStartMsg) {
	r.reviewPending = true
	r.cancelRequested = false
	item := msg.item
	r.reviewItem = &item
	r.selected = 0
	r.reviewPage, r.reviewSeen, r.reviewPages = 0, -1, 0
	r.reviewLayout = ""
}

func (r *toolReviewer) reset() {
	r.mu.Lock()
	r.stopSessionLocked()
	r.mu.Unlock()
	r.reviewPending = false
	r.cancelRequested = false
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
	if r.cancelRequested {
		return true, nil
	}
	options := r.reviewOptions()
	r.normalizeReviewSelection(options)
	switch msg.String() {
	case "pgdown", "down":
		if r.reviewPage+1 < r.reviewPages {
			r.reviewPage++
		}
		return true, nil
	case "pgup", "up":
		if r.reviewPage > 0 {
			r.reviewPage--
		}
		return true, nil
	case "y", "Y":
		if !r.canApprovePages() {
			return true, nil
		}
		r.reviewItem.resp <- reviewResponse{action: reviewActionApprove}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "s", "S":
		if !r.reviewItem.allowSimplify {
			return true, nil
		}
		r.reviewItem.resp <- reviewResponse{action: reviewActionSimplify}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "n", "N":
		r.reviewItem.resp <- reviewResponse{}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "a", "A":
		if !r.canApprovePages() {
			return true, nil
		}
		if len(r.reviewItem.candidateRules) == 0 {
			return true, nil
		}
		r.rules.Add(r.reviewItem.candidateRules...)
		r.reviewItem.resp <- reviewResponse{action: reviewActionApprove}
		r.reviewPending = false
		r.reviewItem = nil
		return true, r.pollReviewCmd()
	case "ctrl+c":
		r.cancelRequested = true
		return true, msgCmd(reviewCancelMsg{})
	case "left":
		r.moveReviewSelection(options, -1)
		return true, nil
	case "right":
		r.moveReviewSelection(options, 1)
		return true, nil
	case "enter":
		if (options[r.selected].action == reviewOptionApprove || options[r.selected].action == reviewOptionAlwaysAllow) && !r.canApprovePages() {
			return true, nil
		}
		switch options[r.selected].action {
		case reviewOptionApprove:
			r.reviewItem.resp <- reviewResponse{action: reviewActionApprove}
		case reviewOptionDeny:
			r.reviewItem.resp <- reviewResponse{}
		case reviewOptionSimplify:
			r.reviewItem.resp <- reviewResponse{action: reviewActionSimplify}
		case reviewOptionAlwaysAllow:
			r.rules.Add(r.reviewItem.candidateRules...)
			r.reviewItem.resp <- reviewResponse{action: reviewActionApprove}
		case reviewOptionCancel:
			r.cancelRequested = true
			return true, msgCmd(reviewCancelMsg{})
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
	if r.reviewItem != nil && r.reviewItem.allowSimplify {
		options = append(options, reviewOption{key: "S", label: "Simplify", action: reviewOptionSimplify})
	}
	if r.reviewItem != nil && len(r.reviewItem.candidateRules) > 0 {
		options = append(options, reviewOption{key: "A", label: "Allow for session", action: reviewOptionAlwaysAllow})
	}
	return append(options, reviewOption{key: "Ctrl+C", label: "Cancel task", action: reviewOptionCancel})
}

func (r *toolReviewer) reviewOptionEnabled(option reviewOption) bool {
	return option.action != reviewOptionApprove && option.action != reviewOptionAlwaysAllow || r.canApprovePages()
}

func (r *toolReviewer) normalizeReviewSelection(options []reviewOption) {
	if r.selected < 0 || r.selected >= len(options) {
		r.selected = 0
	}
	if !r.reviewOptionEnabled(options[r.selected]) {
		r.moveReviewSelection(options, 1)
	}
}

func (r *toolReviewer) moveReviewSelection(options []reviewOption, direction int) {
	for range len(options) {
		r.selected = (r.selected + direction + len(options)) % len(options)
		if r.reviewOptionEnabled(options[r.selected]) {
			return
		}
	}
}

func (r *toolReviewer) reviewActions() []interactionAction {
	options := r.reviewOptions()
	r.normalizeReviewSelection(options)
	var actions []interactionAction
	for i, option := range options {
		if r.reviewOptionEnabled(option) {
			actions = append(actions, interactionAction{Key: option.key, Label: option.label, Selected: i == r.selected})
		}
	}
	return actions
}

// buildAccessIntent derives the unified AccessIntent for a tool call. Shell
// tools consume a completed CommandAssessment; runtime-resolved targets remain
// separate so they cannot become directory rules. Tools with a registered IntentExtractor use that;
// read-only tools without directory semantics become read intents; anything
// else degrades fail-closed to a write with no known dirs.
func buildAccessIntent(name string, data []byte, registry *toolregistry.Registry, assessment *commandanalysis.CommandAssessment) AccessIntent {
	if registry != nil && registry.ShellExecution(name) {
		if assessment == nil {
			unknown := commandanalysis.UnknownCommandAssessment()
			return commandResultToIntent(commandanalysis.Result(unknown))
		}
		intent := commandResultToIntent(commandanalysis.Result(*assessment))
		return intent
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

// normalizeAffectedDirs reduces file paths to their parent directories and
// drops entries already covered by a broader directory, so the review
// summary and candidate DirAllow rule only mention directories. The
// external-path extractor occasionally reports a file path
// alongside (or instead of) its containing directory; without this, the
// "Always allows" line would advertise a file name as if it were a dir.
func normalizeAffectedDirs(dirs []string) []string {
	return normalizeAffectedDirsForWorkingDir(dirs, "")
}

func normalizeAffectedDirsForWorkingDir(dirs []string, cwd string) []string {
	return pathutil.NormalizeDirs(dirs, pathutil.DefaultOptions(cwd, pathutil.FlavorPOSIX))
}

func normalizeShellAffectedDirsForTool(dirs []string, cwd string, tool string) []string {
	return pathutil.NormalizeShellDirs(dirs, pathutil.DefaultOptions(cwd, shellPathFlavor(tool)))
}

func normalizeAccessIntentDirs(intent AccessIntent, cwd, tool string, shell bool) AccessIntent {
	normalize := func(dirs []string) []string {
		if shell {
			return normalizeShellAffectedDirsForTool(dirs, cwd, tool)
		}
		return normalizeAffectedDirsForWorkingDir(dirs, cwd)
	}
	if intent.ReadDirs != nil || intent.WriteDirs != nil {
		if intent.ReadDirs != nil {
			intent.ReadDirs = normalize(intent.ReadDirs)
			if intent.ReadDirs == nil {
				intent.ReadDirs = []string{}
			}
		}
		if intent.WriteDirs != nil {
			intent.WriteDirs = normalize(intent.WriteDirs)
			if intent.WriteDirs == nil {
				intent.WriteDirs = []string{}
			}
		}
	} else if len(intent.Dirs) > 0 {
		intent.Dirs = normalize(intent.Dirs)
	}
	intent.RemoteOrigins = NormalizeRemoteOrigins(intent.RemoteOrigins)
	intent.ReadOrigins = NormalizeRemoteOrigins(intent.ReadOrigins)
	intent.WriteOrigins = NormalizeRemoteOrigins(intent.WriteOrigins)
	return intent
}

// reviewerDeps carries the dependencies requestApproval needs from its
// caller. Keeping them in a struct (instead of reaching into *Mods)
// lets the reviewer stay physically independent of the main model: it
// can be constructed, tested, and reasoned about without a live Mods.
//
// A nil assessment is fail-closed when buildAccessIntent runs; the reviewer
// itself never reparses or reconstructs command facts.
type reviewerDeps struct {
	ctx            context.Context
	shellExecution bool
	assessment     *commandanalysis.CommandAssessment
	accessIntent   AccessIntent
	safeDirs       []string
	payload        **commandPayload
	preparePayload func(context.Context) (*commandPayload, error)
	redact         func(string) string
	executionRows  []interactionRow
	onDecision     func(approvalTrace)
}

type approvalTrace struct {
	Source   string
	Detail   string
	Duration time.Duration
}

func (r *toolReviewer) requestApproval(deps reviewerDeps, name string, data []byte) (returnErr error) {
	started := time.Now()
	trace := approvalTrace{Source: "not reached"}
	defer func() {
		// Cancellation wins even if the session-closed channel or a response
		// becomes ready at the same time. It is not a denial to send to the model.
		if err := deps.ctx.Err(); err != nil {
			returnErr = err
			trace.Source = "cancelled"
		}
		trace.Duration = time.Since(started)
		if deps.onDecision != nil {
			deps.onDecision(trace)
		}
	}()
	safeDirSet := deps.safeDirs
	if len(safeDirSet) == 0 {
		safeDirSet = safeDirs()
	}
	shellExecution := deps.shellExecution
	intent := deps.accessIntent
	assessment := commandanalysis.CommandAssessment{}
	if deps.assessment != nil {
		assessment = *deps.assessment
	}
	if shellExecution {
		intent = normalizeAccessIntentDirs(intent, r.scope.Value, name, true)
		assessment.KnownDirs = normalizeShellAffectedDirsForTool(assessment.KnownDirs, r.scope.Value, name)
		debug.Printf("command assessment: effect=%s evidence=%s coverage=%d gaps=%v dirs=%v", assessment.Effect, assessment.Evidence, assessment.WriteCoverage, assessment.Gaps, assessment.KnownDirs)
	}
	if source := r.reviewBypassSource(intent, safeDirSet); source != "" {
		trace.Source = source
		trace.Detail = accessIntentSummary(intent)
		return nil
	}
	if !r.canReviewInteractively() {
		trace.Source = "unavailable"
		trace.Detail = "no interactive approval channel"
		return fmt.Errorf(
			"%w: %s requires approval, but no interactive approval channel is available; run interactively or use --review-mode never if non-interactive execution is intentional",
			errReviewUnavailable,
			name,
		)
	}
	if deps.payload != nil && *deps.payload == nil && deps.preparePayload != nil {
		payload, err := deps.preparePayload(deps.ctx)
		if err != nil {
			trace.Source = "unavailable"
			trace.Detail = "script payload preparation failed"
			return err
		}
		*deps.payload = payload
	}
	respCh := make(chan reviewResponse, 1)
	var candidateRules []Rule
	if intent.HasAccess() {
		candidateRules = candidateRulesForIntent(intent, r.scope, safeDirSet, ApprovalReviewMode(r.reviewMode))
	}
	presentation := formatReviewPresentationWithIntent(name, data, assessment, r.scope, intent)
	// Redact before quoting/numbering: those transformations can otherwise
	// split a multi-line secret or turn its characters into escape sequences.
	if deps.redact != nil {
		for i := range presentation.rows {
			switch presentation.rows[i].Label {
			case "Source":
				if name == "shell_run" || name == "powershell_run" {
					presentation.rows[i].Value = numberedReviewSource(redactRemoteURLsForDisplay(deps.redact(ExtractShellCommand(data))))
				}
			case "Argv":
				var args commandPayloadArgs
				if name == "process_run" && json.Unmarshal(data, &args) == nil {
					argv := append([]string{args.Program}, args.Args...)
					for j := range argv {
						argv[j] = deps.redact(argv[j])
					}
					presentation.rows[i].Value = quotedReviewArgv(argv)
				}
			}
		}
	}
	if len(deps.executionRows) > 0 {
		rows := presentation.rows[:0]
		for _, row := range presentation.rows {
			if row.Label != "Interpreter" && row.Label != "Working dir" {
				rows = append(rows, row)
			}
		}
		presentation.rows = append(rows, deps.executionRows...)
	}
	if deps.payload != nil {
		presentation.rows = append(presentation.rows, (*deps.payload).reviewRows(deps.redact)...)
	}
	if assessment.Reviewability.ScriptFilePayload && (deps.payload == nil || *deps.payload == nil) {
		presentation.rows = append(presentation.rows, interactionRow{Label: "Script preview", Value: "Unavailable for this invocation form; script content has not been reviewed. Ordinary effect/target approval still applies."})
	}
	for i := range presentation.rows {
		if deps.redact != nil {
			presentation.rows[i].Value = deps.redact(presentation.rows[i].Value)
		}
		presentation.rows[i].Value = redactRemoteURLsForDisplay(presentation.rows[i].Value)
	}
	item := toolReviewItem{
		name:           name,
		args:           data,
		candidateRules: candidateRules,
		summary:        formatReviewSummaryWithIntent(name, data, assessment, r.scope, intent),
		presentation:   presentation,
		allowSimplify:  shellExecution,
		resp:           respCh,
	}
	ch, done := r.snapshotSession()
	if ch == nil {
		trace.Source = "unavailable"
		trace.Detail = "review session ended"
		return fmt.Errorf("%w: %s", errReviewUnavailable, name)
	}
	select {
	case ch <- item:
		trace.Source = "user"
		trace.Detail = "waiting"
	case <-done:
		trace.Source = "unavailable"
		trace.Detail = "review session ended"
		return fmt.Errorf("%w: %s", errReviewUnavailable, name)
	case <-deps.ctx.Done():
		trace.Source = "cancelled"
		trace.Detail = "before prompt"
		return deps.ctx.Err()
	}
	select {
	case response := <-respCh:
		switch response.action {
		case reviewActionApprove:
			trace.Detail = "approved"
			return nil
		case reviewActionSimplify:
			if !shellExecution {
				trace.Detail = "denied"
				return fmt.Errorf("execution denied by user for: %s", name)
			}
			trace.Detail = "simplification requested"
			return &commandReorganizationError{ReasonCodes: []string{"user_requested"}}
		default:
			trace.Detail = "denied"
			return fmt.Errorf("execution denied by user for: %s", name)
		}
	case <-done:
		trace.Source = "unavailable"
		trace.Detail = "review session ended"
		return fmt.Errorf("%w: %s", errReviewUnavailable, name)
	case <-deps.ctx.Done():
		trace.Source = "cancelled"
		trace.Detail = "while waiting for user"
		return deps.ctx.Err()
	}
}

func accessIntentSummary(intent AccessIntent) string {
	groups := intent.Groups()
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		part := string(group.Class)
		if len(group.Dirs) > 0 {
			part += " · " + strings.Join(group.Dirs, ", ")
		}
		if len(group.Origins) > 0 {
			part += " · " + strings.Join(group.Origins, ", ")
		}
		parts = append(parts, part)
	}
	if len(intent.ProviderWriteTargets) > 0 {
		parts = append(parts, "provider write · "+strings.Join(intent.ProviderWriteTargets, ", "))
	}
	return strings.Join(parts, " · ")
}

func candidateRulesForIntent(intent AccessIntent, scope Scope, safeDirs []string, reviewMode ApprovalReviewMode) []Rule {
	if reviewMode != ApprovalReviewMode(ReviewAuto) || intent.HasNonReusableWriteTargets() {
		return nil
	}
	var rules []Rule
	for _, group := range intent.Groups() {
		if group.Class == AccessRead {
			continue
		}
		if group.Class != AccessWrite || len(group.Dirs) == 0 && len(group.Origins) == 0 {
			return nil
		}
		var reviewDirs []string
		for _, dir := range group.Dirs {
			if ClassifyAccess(AccessIntent{Class: AccessWrite, Dirs: []string{dir}, WriteCoverage: approval.CoverageComplete}, scope, safeDirs, reviewMode) == DecisionAsk {
				reviewDirs = append(reviewDirs, dir)
			}
		}
		rules = append(rules, RulesForDirs(reviewDirs, scope, AccessWrite)...)
		rules = append(rules, RulesForRemoteOrigins(group.Origins)...)
	}
	return rules
}

// extractSecretEnvNames returns the environment variable names this call
// injects as secrets, in original and upper-case form. Static environment
// path expansion must skip these names: the child shell would observe the
// secret's value rather than the inherited one classification sees.
func extractSecretEnvNames(data []byte) map[string]bool {
	var parsed struct {
		SecretEnv map[string]string `json:"secret_env"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.SecretEnv) == 0 {
		return nil
	}
	names := make(map[string]bool, 2*len(parsed.SecretEnv))
	for name := range parsed.SecretEnv {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		names[name] = true
		names[strings.ToUpper(name)] = true
	}
	return names
}

func (r *toolReviewer) renderBanner(width int, styles ui.InteractionStyles, heights ...int) string {
	if width <= 0 {
		width = 80
	}
	if pagedReviewTool(r.reviewItem.name) {
		height := 28
		if len(heights) > 0 {
			height = heights[0]
		}
		return r.renderPagedReview(width, height, styles)
	}
	actions := r.reviewActions()
	presentation := r.reviewItem.presentation
	if presentation.headline == "" && r.reviewItem.summary != "" {
		presentation = reviewPresentation{
			tone: interactionToneWarning, toneText: "Warning", headline: r.reviewItem.summary,
		}
	}
	rows := reviewOrderedRows(presentation.rows)
	if len(r.reviewItem.candidateRules) > 0 {
		rows = append(rows, interactionRow{Label: "If A", Value: sessionRuleLabel(r.reviewItem.candidateRules, rows)})
	}
	return renderInteractionPanel(styles, width, interactionPanel{
		Title:    "Review",
		Tone:     presentation.tone,
		ToneText: presentation.toneText,
		Headline: presentation.headline,
		Rows:     rows,
		Actions:  actions,
	})
}
