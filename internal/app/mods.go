package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

type state int

const (
	startState state = iota
	configLoadedState
	planState
	requestState
	responseState
	doneState
	errorState
)

const (
	defaultMaxToolRounds = 30
	defaultFanciness     = 10
	maxToolFailedRounds  = 3
	renderFrameInterval  = time.Second / 30
)

const numPlanReviewOptions = 4

// Mods is the Bubble Tea model that manages reading stdin and querying
// LLM APIs (OpenAI, Anthropic, Google, Ollama, and others).
type Mods struct {
	outputRenderer
	Input          string
	Styles         Styles
	Error          *modsError
	state          state
	retries        int
	toolCallRounds int
	totalRounds    int
	glam           *glamour.TermRenderer
	glamViewport   viewport.Model
	// messages is the session history fed to the provider on each
	// turn. It is mutated by setupStreamContext (in a tea.Cmd goroutine
	// dispatched by startCompletionCmd/startPlanCmd) and re-read from the
	// stream on the Update goroutine after the stream finishes. Concurrent
	// access is serialized by Bubble Tea's program loop: a Cmd goroutine
	// publishes its writes via the returned tea.Msg, and the next Update
	// observes them through Bubble Tea's internal channel send/receive.
	// There is intentionally no per-field mutex; callers must not introduce
	// new background goroutines that touch m.messages outside this pattern.
	messages []proto.Message
	// planHistory snapshots the plan-phase conversation (user request +
	// investigation + proposed plan) when an approved plan transitions to
	// execution, so the execution turn keeps the context gathered during
	// planning instead of re-investigating from scratch after setupStreamContext
	// resets m.messages. System messages are excluded (captured as non-system),
	// which also strips the plan-mode "PLANNING PHASE ONLY" prompt.
	planHistory             []proto.Message
	cancelRequest           []context.CancelFunc
	cancelMu                sync.Mutex
	anim                    tea.Model
	activeOperation         string
	thinkActive             bool
	responseOutputStarted   bool
	responseBoundaryPending bool
	width                   int
	height                  int
	showOperationStatus     bool
	Thought                 string
	thoughtFlushed          bool
	tokenUsage              proto.TokenUsage

	db     *DB
	Config *Config

	content             []string
	contentMutex        *sync.Mutex
	operationMutex      sync.Mutex
	toolOperations      chan<- toolOperationStatusMsg
	currentToolRegistry *toolregistry.Registry

	// sessionMu guards activeRunner. activeRunner tracks the streamRunner
	// owning the in-flight provider stream (if any) so quit() and
	// startCompletionCmd can cancel the stream's context and release HTTP/SSE
	// + MCP resources rather than waiting for the provider goroutine to
	// finish on its own.
	sessionMu    sync.Mutex
	activeRunner *streamRunner

	stdinImageData []byte

	ctx context.Context

	reviewer  *toolReviewer
	userInput *userInputManager
	secrets   *secrets.Store

	shellAnalyzer func(tool, command string) shellCommandAnalysis

	planContent  string
	planRetries  int
	planSelected int

	proposals        []proposal
	proposalSelected int
	proposalMode     bool

	feedbackInput textinput.Model
	feedbackMode  bool

	// skillCatalog is the result of skills.ScanDirs(cfg.ResolveSkillsDirs()) at New()
	// time. Empty when the skills directories are absent or have no skills;
	// in that case no catalog is injected and load_skill is not
	// registered. See docs/superpowers/specs/2026-07-06-skills-system-design.md.
	skillCatalog []skills.Skill
}

func New(
	ctx context.Context,
	cfg *Config,
	db *DB,
) (*Mods, error) {
	wordWrap := cfg.WordWrap
	opts := []glamour.TermRendererOption{
		glamour.WithEnvironmentConfig(),
	}
	if wordWrap > 0 {
		opts = append(opts, glamour.WithWordWrap(wordWrap))
	}
	gr, err := glamour.NewTermRenderer(opts...)
	if err != nil {
		return nil, fmt.Errorf("could not create glamour renderer: %w", err)
	}
	vp := viewport.New()
	vp.GotoBottom()
	skillDirs := cfg.ResolveSkillsDirs()
	skillCatalog, scanErr := skills.ScanDirs(skillDirs)
	if scanErr != nil {
		debug.Printf("skills: scan %v failed: %v (proceeding with empty catalog)", skillDirs, scanErr)
	}
	debug.Printf("Skills: loaded %d skill(s) from %v", len(skillCatalog), skillDirs)
	return &Mods{
		Styles:              ui.MakeStylesWithTheme(cfg.Theme, true),
		glam:                gr,
		state:               startState,
		glamViewport:        vp,
		contentMutex:        &sync.Mutex{},
		showOperationStatus: IsOutputTTY() && IsErrorTTY() && !cfg.Raw && !cfg.HideToolStatus,
		db:                  db,
		Config:              cfg,
		reviewer:            newToolReviewer(cfg),
		userInput:           newUserInputManager(cfg),
		secrets:             secrets.New(),
		ctx:                 ctx,
		skillCatalog:        skillCatalog,
	}, nil
}

func (m *Mods) Err() *modsError {
	return m.Error
}

func (m *Mods) RenderedOutput() string {
	return m.glamOutput
}

// RenderMarkdown renders standalone Markdown with the same Glamour renderer
// and word-wrap configuration used for model responses. It does not mutate
// the conversation's accumulated or rendered output.
func (m *Mods) RenderMarkdown(content string) (string, error) {
	return m.glam.Render(content)
}

func (m *Mods) Messages() []proto.Message {
	return append([]proto.Message(nil), m.messages...)
}

// TokenUsage returns token consumption accumulated for this interaction.
func (m *Mods) TokenUsage() proto.TokenUsage { return m.tokenUsage }

func (m *Mods) ApprovalRules() []Rule {
	return m.reviewer.rules.Snapshot()
}

// Init implements tea.Model.
func (m *Mods) Init() tea.Cmd {
	// Terminal queries are only safe when the CLI has an interactive input
	// reader available to consume their replies. Otherwise those replies can
	// arrive after the program exits and be echoed by the user's shell.
	if m.Config.Raw || !m.Config.InteractiveTTYAvailable {
		return m.findSessionDetails()
	}
	return tea.Batch(m.findSessionDetails(), tea.RequestBackgroundColor)
}

// Update implements tea.Model.
func (m *Mods) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg, ok := msg.(tea.BackgroundColorMsg); ok {
		m.Styles = ui.MakeStylesWithTheme(m.Config.Theme, msg.IsDark())
	}

	if inputMsg, ok := msg.(tea.KeyMsg); ok {
		if handled, cmd := m.userInput.handleKey(inputMsg); handled {
			return m, cmd
		}
	}

	if m.feedbackMode {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			switch msg.String() {
			case "enter":
				m.feedbackMode = false
				feedback := strings.TrimSpace(m.feedbackInput.Value())
				if feedback == "" {
					return m, nil
				}
				return m, msgCmd(planModifyMsg{
					feedback: feedback,
					plan:     m.planContent,
				})
			case "esc":
				m.feedbackMode = false
				return m, nil
			default:
				var cmd tea.Cmd
				m.feedbackInput, cmd = m.feedbackInput.Update(msg)
				return m, cmd
			}
		default:
			var cmd tea.Cmd
			m.feedbackInput, cmd = m.feedbackInput.Update(msg)
			return m, cmd
		}
	}

	switch msg := msg.(type) {
	case sessionDetailsMsg:
		m.Config.SessionWriteToID = msg.WriteID
		m.Config.SessionWriteToTitle = msg.Title
		m.Config.SessionReadFromID = msg.ReadID
		m.reviewer.rules.Replace(msg.Rules)

		m.anim = NewAnim(defaultFanciness, m.Styles)
		cmds = append(cmds, m.anim.Init())
		m.state = configLoadedState
		cmds = append(cmds, m.readStdinCmd)

	case stdinImageInput:
		m.stdinImageData = msg.data
		if m.Config.Prefix == "" {
			return m, m.quit
		}
		if m.Config.Dirs ||
			m.Config.ShowHelp ||
			m.Config.List ||
			m.Config.ListRoles ||
			m.Config.ListPrompts ||
			m.Config.ListSkills ||
			m.Config.Settings ||
			m.Config.ConfigSetup ||
			m.Config.ResetSettings {
			return m, m.quit
		}
		m.state = requestState
		cmds = append(cmds, m.startCompletionCmd(""))

	case retryMsg:
		// Schedule the retry via tea.Tick so the Update loop remains
		// responsive during the back-off. If the user quits before the tick
		// fires, the resulting completionInput is delivered to a stopped
		// Program and harmlessly dropped by Bubble Tea.
		return m, tea.Tick(msg.wait, func(time.Time) tea.Msg {
			return completionInput{content: msg.content}
		})

	case completionInput:
		if msg.content != "" {
			m.Input = RemoveWhitespace(msg.content)
		}
		if m.Input == "" && m.Config.Prefix == "" {
			return m, m.quit
		}
		if m.Config.Dirs ||
			m.Config.ShowHelp ||
			m.Config.List ||
			m.Config.ListRoles ||
			m.Config.ListPrompts ||
			m.Config.ListSkills ||
			m.Config.Settings ||
			m.Config.ConfigSetup ||
			m.Config.ResetSettings {
			return m, m.quit
		}

		if m.Config.Plan {
			m.state = planState
			m.planRetries = 0
			cmds = append(cmds, m.startPlanCmd(msg.content))
		} else {
			m.state = requestState
			cmds = append(cmds, m.startCompletionCmd(msg.content))
		}
	case streamEventMsg:
		switch msg.kind {
		case streamEventChunk:
			if msg.chunk.Thought != "" {
				m.Thought += msg.chunk.Thought
				if m.thinkActive {
					debug.Printf("Thought: %s", msg.chunk.Thought)
				}
			}
			if msg.chunk.Content != "" {
				cmds = append(cmds, m.handleStreamChunk(msg))
			} else {
				cmds = append(cmds, msg.runner.receiveCmd())
			}
		case streamEventToolCallsStart:
			cmds = append(cmds, m.startToolCalls(msg.runner)...)
		case streamEventToolCalls:
			return m, m.handleToolCallsDone(msg)
		case streamEventDone:
			m.tokenUsage.Add(msg.runner.takeUsage())
			if m.Config.Plan {
				return m, msgCmd(planCompleteMsg{plan: m.Output})
			}
			m.state = doneState
			return m, m.quit
		case streamEventError:
			return m, msgCmd(msg.runner.errh(msg.err))
		}
	case toolOperationStatusMsg:
		if msg.done {
			m.setActiveOperation("")
			break
		}
		m.setActiveOperation(msg.content)
		if msg.ch != nil {
			cmds = append(cmds, m.pollToolOperationStatusCmd(msg.ch))
		}
	case toolReviewStartMsg:
		m.reviewer.handleStartMsg(msg)
		m.setActiveOperation("")
	case userInputStartMsg:
		m.userInput.handleStartMsg(msg)
		m.setActiveOperation("")
	case planCompleteMsg:
		if !looksLikePlan(msg.plan) {
			m.setActiveOperation("")
			return m, msgCmd(modsError{
				Err:        errors.New("no plan generated"),
				ReasonText: "The model finished without producing a plan — its investigation may have been interrupted or it stopped early. Re-run, or rephrase the request.",
			})
		}
		m.planContent = msg.plan
		m.proposals = parseProposals(msg.plan)
		if len(m.proposals) > 0 {
			m.proposalMode = true
			m.showProposal(0)
		}
		m.setActiveOperation("")
		m.state = planState
		m.planSelected = 0
		return m, nil
	case planApprovedMsg:
		m.clearProposals()
		transcript := m.approvedPlanTranscript()
		m.planContent = msg.plan
		m.Config.Plan = false
		return m, tea.Sequence(
			m.approvedPlanPrintCmd(transcript),
			msgCmd(planExecutionStartMsg{}),
		)
	case planExecutionStartMsg:
		// Plan and execution are independent API-call lineages. Resetting
		// retries here ensures the exec phase gets the full back-off budget
		// even if the plan phase had to retry on rate limits or transient
		// server errors. planRetries is intentionally untouched: it tracks
		// "user rejected the plan, try a different one" attempts, which are
		// orthogonal to API retry policy.
		m.retries = 0
		m.resetExecutionOutput()
		m.state = requestState
		return m, m.startCompletionCmd(m.Input)
	case planDeniedMsg:
		m.clearProposals()
		m.planRetries++
		if m.planRetries >= maxPlanRetries {
			m.state = doneState
			return m, m.quit
		}
		m.resetOutputBuffers()
		m.planContent = ""
		// A fresh planning attempt starts a new API-call lineage; clear the
		// retry counter so prior plan attempts do not steal back-off budget.
		m.retries = 0
		m.state = planState
		return m, m.startPlanCmd("The previous plan was rejected. Please create a completely different plan.")
	case planModifyMsg:
		m.clearProposals()
		m.resetOutputBuffers()
		m.planContent = ""
		m.planRetries = 0
		// User-driven plan revision is also a fresh API-call lineage.
		m.retries = 0
		m.state = planState
		reviseContent := fmt.Sprintf(
			"Revise the plan based on this feedback: %s\n\nFor reference, the previous plan was:\n%s",
			msg.feedback, msg.plan,
		)
		return m, m.startPlanCmd(reviseContent)
	case modsError:
		m.Error = &msg
		m.state = errorState
		return m, m.quit
	case error:
		m.Error = &modsError{Err: msg, ReasonText: msg.Error()}
		m.state = errorState
		return m, m.quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.glamViewport.SetWidth(m.width)
		m.glamViewport.SetHeight(m.height)
		return m, nil
	case tea.KeyMsg:
		if cmd, handled := m.handleProposalKey(msg); handled {
			return m, cmd
		}
		if cmd, handled := m.handlePlanReviewKey(msg); handled {
			return m, cmd
		}
		if handled, cmd := m.reviewer.handleKey(msg); handled {
			return m, cmd
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.state = doneState
			return m, m.quit
		}
	}
	if m.shouldUpdateAnimation() {
		var cmd tea.Cmd
		m.anim, cmd = m.anim.Update(msg)
		cmds = append(cmds, cmd)
	}
	if m.viewportNeeded() {
		// Only respond to keypresses when the viewport (i.e. the content) is
		// taller than the window.
		var cmd tea.Cmd
		m.glamViewport, cmd = m.glamViewport.Update(msg)
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m *Mods) shouldUpdateAnimation() bool {
	return m.spinnerVisible()
}

func msgCmd(msg tea.Msg) tea.Cmd {
	return func() tea.Msg {
		return msg
	}
}

func (m *Mods) addCancel(cancel context.CancelFunc) {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	m.cancelRequest = append(m.cancelRequest, cancel)
}

// setActiveRunner registers the runner that owns the current in-flight stream
// so quit() and the next startCompletion/startPlan can cancel + release it.
// Replaces any previously registered runner without closing it; callers that
// need to swap should call closeActiveRunner() first.
func (m *Mods) setActiveRunner(r *streamRunner) {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	m.activeRunner = r
}

// takeActiveRunner atomically clears m.activeRunner and returns the previous
// value (possibly nil). Use it before close()ing to avoid double-close races
// with the natural stream completion path.
func (m *Mods) takeActiveRunner() *streamRunner {
	m.sessionMu.Lock()
	defer m.sessionMu.Unlock()
	r := m.activeRunner
	m.activeRunner = nil
	return r
}

// closeActiveRunner takes ownership of the current active runner (if any) and
// closes it. streamRunner.close() is idempotent so it is safe to call this
// even when the natural completion path may close the runner concurrently.
func (m *Mods) closeActiveRunner() {
	if r := m.takeActiveRunner(); r != nil {
		r.close()
	}
}

func (m *Mods) quit() tea.Msg {
	// Tear down the in-flight stream (cancels its context, releases HTTP
	// body + MCP resources) before draining the cancel slice for any tool
	// calls. close() is idempotent so racing with receiveCmd's error path
	// is harmless.
	m.closeActiveRunner()
	m.userInput.reset()
	if m.secrets != nil {
		m.secrets.Clear()
	}
	m.cancelMu.Lock()
	cancels := m.cancelRequest
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	return tea.Quit()
}

func (m *Mods) toolRoundLimitExceeded(maxTotal int, st stream.Stream) bool {
	if m.toolCallRounds > maxToolFailedRounds {
		debug.Printf("Tool call failed rounds exceeded limit (%d), stopping", maxToolFailedRounds)
		m.resetAndOutput(st)
		return true
	}
	if m.totalRounds > maxTotal {
		debug.Printf("Tool call total rounds exceeded limit (%d), stopping", maxTotal)
		m.resetAndOutput(st)
		return true
	}
	return false
}

func (m *Mods) resetAndOutput(st stream.Stream) {
	m.messages = st.Messages()
	content := lastAssistantContent(m.messages)
	if content != "" {
		m.appendToOutput(content)
	}
}

func (m *Mods) handleStreamChunk(msg streamEventMsg) tea.Cmd {
	content := msg.chunk.Content
	// Trim leading whitespace from the very first answer chunk so a newline left
	// over after a stripped </think> block does not render as a blank line above
	// the answer.
	if !m.responseOutputStarted && m.Output == "" {
		content = strings.TrimLeft(content, "\r\n")
	}
	if m.responseBoundaryPending {
		content = strings.TrimLeft(content, "\r\n")
		if content != "" {
			m.appendResponseBoundary()
			m.responseBoundaryPending = false
		}
	}
	if content == "" {
		return msg.runner.receiveCmd()
	}
	m.responseOutputStarted = true
	m.setActiveOperation("")
	if m.thinkActive && !m.thoughtFlushed {
		m.flushThought()
	} else if !m.thinkActive && !m.thoughtFlushed && strings.TrimSpace(m.Thought) != "" {
		debug.Printf("Think: model emitted %d chars of thinking without -t (discarded; pass -t to display)", len(strings.TrimSpace(m.Thought)))
		m.thoughtFlushed = true
	}
	m.appendToOutput(content)
	// Always use responseState while streaming, even in plan mode. The
	// planState View renders only the spinner animation until planContent
	// is set (by planCompleteMsg), which would hide the streaming output.
	// startToolCalls also uses responseState, so using it here keeps the state
	// stable across text→tool→text rounds instead of flickering between
	// planState (output hidden) and responseState (output shown).
	m.state = responseState
	return msg.runner.receiveCmd()
}

func (m *Mods) startToolCalls(runner *streamRunner) []tea.Cmd {
	// The model may reason and then immediately call a tool without emitting any
	// answer text; flush the thought so it is still shown.
	if m.thinkActive && !m.thoughtFlushed {
		m.flushThought()
	} else if !m.thinkActive && !m.thoughtFlushed && strings.TrimSpace(m.Thought) != "" {
		debug.Printf("Think: model emitted %d chars of thinking without -t (discarded; pass -t to display)", len(strings.TrimSpace(m.Thought)))
		m.thoughtFlushed = true
	}
	ch := make(chan toolOperationStatusMsg, 8)
	m.setToolOperationChannel(ch)
	m.setActiveOperation("Running tools")
	m.state = responseState
	cmds := []tea.Cmd{m.pollToolOperationStatusCmd(ch), m.callToolsCmd(runner, ch)}
	cmds = append(cmds[:1], append([]tea.Cmd{m.reviewer.startSession(), m.userInput.startSession()}, cmds[1:]...)...)
	return cmds
}

// shellExitCoder is satisfied by errors that merely carry a non-zero process
// exit code (e.g. tools.ShellExitError). A non-zero shell exit is a normal
// command outcome (no match, file not found, etc.), not a tool-execution
// failure, so it must not consume the failed-round budget used to break out
// of genuine error loops.
type shellExitCoder interface{ ExitCode() int }

// toolCallFailed reports whether a tool result error is a genuine execution
// failure. A normal non-zero shell exit is treated as a successful execution
// that happened to return a non-zero status, so it does not count as a failure.
func toolCallFailed(err error) bool {
	if err == nil {
		return false
	}
	var exitErr shellExitCoder
	return !errors.As(err, &exitErr)
}

func (m *Mods) handleToolCallsDone(msg streamEventMsg) tea.Cmd {
	m.setActiveOperation("")
	m.reviewer.reset()
	m.userInput.reset()
	completionStatus := ""
	for _, call := range msg.results {
		if !errors.Is(call.Err, errReviewUnavailable) {
			m.appendShellResult(call.Name, call.Arguments, call.Err)
			if status := shellCompletionStatus(call.Name, call.Arguments, call.Err, m.width); status != "" {
				completionStatus = status
			}
		}
		if call.Err != nil {
			var exitErr shellExitCoder
			if errors.As(call.Err, &exitErr) {
				debug.Printf("Tool non-zero exit: %s -> status %d, output: %s",
					call.Name, exitErr.ExitCode(), debug.Truncate(call.Output, 500))
			} else {
				debug.Printf("Tool call FAILED: %s -> %v", call.Name, call.Err)
			}
			if errors.Is(call.Err, errReviewUnavailable) {
				msg.runner.close()
				m.currentToolRegistry = nil
				return msgCmd(modsError{
					Err:        call.Err,
					ReasonText: "Tool execution requires review.",
				})
			}
			continue
		}
		argPreview := debug.Truncate(string(call.Arguments), 120)
		outputPreview := debug.Truncate(call.Output, 200)
		if argPreview != "" {
			if outputPreview != "" {
				debug.Printf("Tool call: %s(%s) -> output: %s", call.Name, argPreview, outputPreview)
			} else {
				debug.Printf("Tool call: %s(%s)", call.Name, argPreview)
			}
		} else {
			if outputPreview != "" {
				debug.Printf("Tool call: %s -> output: %s", call.Name, outputPreview)
			} else {
				debug.Printf("Tool call: %s", call.Name)
			}
		}
	}
	if len(msg.results) == 0 {
		msg.runner.close()
		m.messages = msg.runner.messages()
		m.toolCallRounds = 0
		m.totalRounds = 0
		// The next generation round renders the spinner animation on its
		// own; mirroring a status line here would produce a duplicate row
		// while waiting for the model to respond.
		m.setActiveOperation("")
		return msgCmd(msg.runner.doneMsg())
	}
	m.setActiveOperation(completionStatus)
	m.totalRounds++
	hasFailed := slices.ContainsFunc(msg.results, func(c proto.ToolCallStatus) bool {
		return toolCallFailed(c.Err)
	})
	if hasFailed {
		m.toolCallRounds++
	}
	maxTotal := m.Config.MaxToolRounds
	if maxTotal <= 0 {
		maxTotal = defaultMaxToolRounds
	}
	if m.toolRoundLimitExceeded(maxTotal, msg.runner.stream) {
		msg.runner.close()
		return msgCmd(msg.runner.doneMsg())
	}
	debug.Printf("Tool call round %d (total=%d/%d, failed=%d/%d)", m.toolCallRounds, m.totalRounds, maxTotal, m.toolCallRounds, maxToolFailedRounds)
	m.responseBoundaryPending = true
	return msg.runner.receiveCmd()
}

func (m *Mods) clearProposals() {
	m.proposalMode = false
	m.proposals = nil
}

func (m *Mods) handleProposalKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if !m.proposalMode || len(m.proposals) == 0 {
		return nil, false
	}
	switch msg.String() {
	case "left":
		idx := m.proposalSelected - 1
		if idx < 0 {
			idx = len(m.proposals) - 1
		}
		m.showProposal(idx)
		return nil, true
	case "right":
		idx := m.proposalSelected + 1
		if idx >= len(m.proposals) {
			idx = 0
		}
		m.showProposal(idx)
		return nil, true
	case "enter", "y", "Y":
		// Enter is shorthand for Y (select the current proposal). The
		// previous switch on m.planSelected was dead code: no key in
		// proposal mode could move planSelected away from 0, so cases 1-4
		// were unreachable and the case-0 branch left planSelected=1 which
		// no subsequent enter could observe.
		m.planContent = m.proposals[m.proposalSelected].content
		m.clearProposals()
		return nil, true
	case "m", "M":
		ti := textinput.New()
		ti.Placeholder = "Describe changes you want to make to this proposal..."
		ti.SetWidth(max(m.width-4, 20))
		ti.SetVirtualCursor(false)
		m.feedbackInput = ti
		m.feedbackMode = true
		return m.feedbackInput.Focus(), true
	case "n", "N":
		m.clearProposals()
		return msgCmd(planDeniedMsg{content: m.Config.Prefix}), true
	case "ctrl+c":
		m.state = doneState
		return m.quit, true
	}
	return nil, true
}

func (m *Mods) handlePlanReviewKey(msg tea.KeyMsg) (tea.Cmd, bool) {
	if m.proposalMode || strings.TrimSpace(m.planContent) == "" {
		return nil, false
	}
	switch msg.String() {
	case "y", "Y":
		return msgCmd(planApprovedMsg{plan: m.planContent}), true
	case "n", "N":
		return msgCmd(planDeniedMsg{content: m.Config.Prefix}), true
	case "m", "M":
		ti := textinput.New()
		ti.Placeholder = "Describe changes you want to make to the plan..."
		ti.SetWidth(max(m.width-4, 20))
		ti.SetVirtualCursor(false)
		m.feedbackInput = ti
		m.feedbackMode = true
		return m.feedbackInput.Focus(), true
	case "ctrl+c":
		m.state = doneState
		return m.quit, true
	case "left":
		m.planSelected--
		if m.planSelected < 0 {
			m.planSelected = numPlanReviewOptions - 1
		}
		return nil, true
	case "right":
		m.planSelected++
		if m.planSelected >= numPlanReviewOptions {
			m.planSelected = 0
		}
		return nil, true
	case "enter":
		switch m.planSelected {
		case 0:
			return msgCmd(planApprovedMsg{plan: m.planContent}), true
		case 1:
			return msgCmd(planDeniedMsg{content: m.Config.Prefix}), true
		case 2:
			ti := textinput.New()
			ti.Placeholder = "Describe changes you want to make to the plan..."
			ti.SetWidth(max(m.width-4, 20))
			ti.SetVirtualCursor(false)
			m.feedbackInput = ti
			m.feedbackMode = true
			return m.feedbackInput.Focus(), true
		case 3:
			m.state = doneState
			return m.quit, true
		}
	}
	return nil, false
}
