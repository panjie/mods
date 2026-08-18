package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	glamourstyles "charm.land/glamour/v2/styles"
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
)

type state int

const (
	startState state = iota
	configLoadedState
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
	terminalProbeTimeout = 250 * time.Millisecond
)

// Mods is the Bubble Tea model that manages reading stdin and querying
// LLM APIs (OpenAI, Anthropic, Google, Ollama, and others).
type Mods struct {
	outputRenderer
	Input          string
	Styles         Styles
	Error          *modsError
	state          state
	terminalReady  bool
	retries        int
	toolCallRounds int
	totalRounds    int
	glam           *glamour.TermRenderer
	glamViewport   viewport.Model
	// messages is the session history fed to the provider on each
	// turn. It is mutated by setupStreamContext (in a tea.Cmd goroutine
	// dispatched by startCompletionCmd) and re-read from the
	// stream on the Update goroutine after the stream finishes. Concurrent
	// access is serialized by Bubble Tea's program loop: a Cmd goroutine
	// publishes its writes via the returned tea.Msg, and the next Update
	// observes them through Bubble Tea's internal channel send/receive.
	// There is intentionally no per-field mutex; callers must not introduce
	// new background goroutines that touch m.messages outside this pattern.
	messages                []proto.Message
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
	// todoItems mirrors the most recent todo_write plan so the footer can
	// render persistent progress. Updated on the Update goroutine from
	// handleToolCallsDone; reset per turn by setupStreamContext when the
	// previous plan completed, and restored from session history on
	// --continue while still in progress.
	todoItems          []ui.TodoItem
	tokenUsage         proto.TokenUsage
	debugTurn          int
	debugRound         int
	debugTurnStarted   time.Time
	debugTurnActive    bool
	debugRoundStarted  time.Time
	debugThoughtStart  int
	debugActivities    []string
	debugToolTotal     int
	debugToolRounds    int
	debugToolSucceeded int
	debugToolExited    int
	debugToolFailed    int
	debugToolDenied    int
	debugToolCorrected int
	debugToolCancelled int

	db     *DB
	Config *Config

	content               []string
	contentMutex          *sync.Mutex
	operationMutex        sync.Mutex
	toolOperations        chan<- toolOperationStatusMsg
	currentToolRegistry   *toolregistry.Registry
	selfHelpFallback      string
	selfHelpReference     selfhelp.Reference
	toolSelectionInsertAt int

	// sessionMu guards activeRunner. activeRunner tracks the streamRunner
	// owning the in-flight provider stream (if any) so quit() and
	// startCompletionCmd can cancel the stream's context and release HTTP/SSE
	// + MCP resources rather than waiting for the provider goroutine to
	// finish on its own.
	sessionMu    sync.Mutex
	activeRunner *streamRunner

	stdinImageData []byte

	ctx context.Context

	// ctxCancel cancels ctx. Invoked by quit() so every provider HTTP request,
	// tool call, shell-classifier call, and approval/user-input select derived
	// from ctx aborts promptly when the user quits (Ctrl+C) instead of leaking
	// until process exit. Nil when *Mods is built without New (tests);
	// cancelContext is nil-safe.
	ctxCancel context.CancelFunc

	reviewer  *toolReviewer
	userInput *userInputManager
	secrets   *secrets.Store

	shellAnalyzer func(tool, command string) approval.CommandAssessment

	// skillCatalog merges binary-embedded skills with the result of
	// skills.ScanDirs(cfg.ResolveSkillsDirs()) at New() time. User skills have
	// precedence over same-name built-ins.
	skillCatalog []skills.Skill
}

func New(
	ctx context.Context,
	cfg *Config,
	db *DB,
	options ...Option,
) (*Mods, error) {
	var newOptions newOptions
	for _, option := range options {
		option(&newOptions)
	}
	isDark := ui.StderrIsDark()
	gr, err := newMarkdownRenderer(cfg.WordWrap, isDark)
	if err != nil {
		return nil, fmt.Errorf("could not create glamour renderer: %w", err)
	}
	vp := viewport.New()
	vp.GotoBottom()
	skillDirs := cfg.ResolveSkillsDirs()
	userSkillCatalog, scanErr := skills.ScanDirs(skillDirs)
	if scanErr != nil {
		debug.Printf("skills: scan %v failed: %v (proceeding with built-in catalog)", skillDirs, scanErr)
	}
	skillCatalog := skills.MergeCatalog(skills.Builtin(), userSkillCatalog)
	debug.Printf("Skills: loaded %d skill(s) from %v", len(skillCatalog), skillDirs)
	selfHelpReference, err := buildSelfHelpReference(newOptions.selfHelpFlags)
	if err != nil {
		return nil, fmt.Errorf("could not build self-help reference: %w", err)
	}
	// Derive a cancellable child of ctx so quit() can abort every in-flight
	// provider HTTP request, tool call, shell-classifier call, and approval/
	// user-input select (all descend from m.ctx) when the user quits. The
	// parent ctx is untouched, so cancelling m.ctx does not affect any caller.
	requestCtx, requestCancel := context.WithCancel(ctx)
	return &Mods{
		Styles:              ui.MakeStylesWithTheme(cfg.Theme, isDark),
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
		ctx:                 requestCtx,
		ctxCancel:           requestCancel,
		skillCatalog:        skillCatalog,
		selfHelpReference:   selfHelpReference,
	}, nil
}

func newMarkdownRenderer(wordWrap int, isDark bool) (*glamour.TermRenderer, error) {
	styleOption := glamour.WithEnvironmentConfig()
	if os.Getenv("GLAMOUR_STYLE") == "" {
		style := glamourstyles.LightStyle
		if isDark {
			style = glamourstyles.DarkStyle
		}
		styleOption = glamour.WithStandardStyle(style)
	}

	opts := []glamour.TermRendererOption{styleOption}
	if wordWrap > 0 {
		opts = append(opts, glamour.WithWordWrap(wordWrap))
	}
	return glamour.NewTermRenderer(opts...)
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
		m.terminalReady = true
		return m.findSessionDetails()
	}
	// Bubble Tea queries terminal modes 2026 and 2027 before running Init.
	// Wait for the subsequently requested background-color reply before doing
	// work that can fail and quit immediately. Terminal replies are ordered,
	// so receiving BackgroundColorMsg also means the earlier mode replies have
	// been consumed by Bubble Tea's input loop.
	return tea.Batch(
		tea.RequestBackgroundColor,
		tea.Tick(terminalProbeTimeout, func(time.Time) tea.Msg {
			return terminalProbeTimeoutMsg{}
		}),
	)
}

func (m *Mods) continueAfterTerminalProbe() tea.Cmd {
	if m.terminalReady {
		return nil
	}
	m.terminalReady = true
	return m.findSessionDetails()
}

// Update implements tea.Model.
func (m *Mods) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	if msg, ok := msg.(tea.BackgroundColorMsg); ok {
		isDark := msg.IsDark()
		m.Styles = ui.MakeStylesWithTheme(m.Config.Theme, isDark)
		gr, err := newMarkdownRenderer(m.Config.WordWrap, isDark)
		if err != nil {
			return m, msgCmd(modsError{
				Err:        err,
				ReasonText: "Could not update Markdown renderer for the terminal background.",
			})
		}
		m.glam = gr
		if cmd := m.continueAfterTerminalProbe(); cmd != nil {
			return m, cmd
		}
	}
	if _, ok := msg.(terminalProbeTimeoutMsg); ok {
		if cmd := m.continueAfterTerminalProbe(); cmd != nil {
			return m, cmd
		}
	}

	if inputMsg, ok := msg.(tea.KeyMsg); ok {
		if handled, cmd := m.userInput.handleKey(inputMsg); handled {
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

		m.state = requestState
		cmds = append(cmds, m.startCompletionCmd(msg.content))
	case streamEventMsg:
		switch msg.kind {
		case streamEventChunk:
			if msg.chunk.Activity != "" {
				m.setActiveOperation(msg.chunk.Activity)
				if len(m.debugActivities) == 0 || m.debugActivities[len(m.debugActivities)-1] != msg.chunk.Activity {
					m.debugActivities = append(m.debugActivities, msg.chunk.Activity)
				}
			}
			if msg.chunk.Thought != "" {
				m.Thought += msg.chunk.Thought
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
			usage := msg.runner.takeUsage()
			m.tokenUsage.Add(usage)
			if usage.Available() {
				debug.Printf("token usage: input=%d cached_input=%d output=%d reasoning_output=%d total=%d",
					usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningOutputTokens, usage.TotalTokens)
			}
			m.debugEndTurn("complete", nil)
			m.state = doneState
			return m, m.quit
		case streamEventError:
			m.debugEndModelRound("error", 0, msg.err)
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
	case modsError:
		m.debugEndTurn("failed", msg.Err)
		m.Error = &msg
		m.state = errorState
		return m, m.quit
	case error:
		m.debugEndTurn("failed", msg)
		m.Error = &modsError{Err: msg, ReasonText: msg.Error()}
		m.state = errorState
		return m, m.quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.glamViewport.SetWidth(m.width)
		m.glamViewport.SetHeight(m.height)
		return m, nil
	case tea.KeyMsg:
		if handled, cmd := m.reviewer.handleKey(msg); handled {
			return m, cmd
		}
		switch msg.String() {
		case "q", "ctrl+c":
			m.debugEndTurn("cancelled", context.Canceled)
			m.state = doneState
			return m, m.quit
		}
	}
	// Always forward messages to the animation so its self-rescheduling tick
	// chain survives hidden periods (pending user-input forms, approval
	// banners). If a tick message were dropped while the footer
	// is taken over by one of those surfaces, the chain would break and the
	// spinner would render a frozen frame forever once it becomes visible
	// again. Rendering stays gated by spinnerVisible in footerView; only the
	// tick chain is kept alive unconditionally.
	if m.anim != nil {
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

// cancelContext cancels the request context (m.ctx), aborting every in-flight
// provider HTTP request, tool call, shell-classifier call, and approval/user-
// input select derived from it. Nil-safe so tests that build *Mods directly
// (without New) can still call quit().
func (m *Mods) cancelContext() {
	if m.ctxCancel != nil {
		m.ctxCancel()
	}
}

// setActiveRunner registers the runner that owns the current in-flight stream
// so quit() and the next startCompletion can cancel + release it.
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
	// Cancel the request context first so every in-flight provider HTTP
	// request, tool call, shell-classifier call, and approval/user-input
	// select derived from m.ctx aborts immediately. This closes the window
	// before setActiveRunner registers the stream runner, where
	// closeActiveRunner and the cancelRequest slice are both still empty and
	// would otherwise let an already-spawned completion goroutine continue
	// making HTTP requests against a context that never cancels.
	m.cancelContext()
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
		m.thoughtFlushed = true
	}
	m.appendToOutput(content)
	// Use responseState while streaming so accumulated output remains visible
	// across text→tool→text rounds.
	m.state = responseState
	return msg.runner.receiveCmd()
}

func (m *Mods) startToolCalls(runner *streamRunner) []tea.Cmd {
	m.debugEndModelRound("response received", 0, nil)
	// The model may reason and then immediately call a tool without emitting any
	// answer text; flush the thought so it is still shown.
	if m.thinkActive && !m.thoughtFlushed {
		m.flushThought()
	} else if !m.thinkActive && !m.thoughtFlushed && strings.TrimSpace(m.Thought) != "" {
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

type correctionSuggester interface{ CorrectionSuggested() bool }

// toolCallFailed reports whether a tool result error is a genuine execution
// failure. A normal non-zero shell exit is treated as a successful execution
// that happened to return a non-zero status, so it does not count as a failure.
func toolCallFailed(err error) bool {
	if err == nil {
		return false
	}
	var correction correctionSuggester
	if errors.As(err, &correction) && correction.CorrectionSuggested() {
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
	var outputCmds []tea.Cmd
	for _, call := range msg.results {
		if call.Name == toolregistry.TodoWriteToolName && call.Err == nil {
			if items := ui.TodoItemsFromArgs(call.Arguments); len(items) > 0 {
				m.todoItems = items
			}
		}
		if !errors.Is(call.Err, errReviewUnavailable) {
			outputCmds = append(outputCmds, m.toolResultOutputCmd(call.Name, call.Arguments, call.Err))
			if status := toolCompletionStatus(call.Name, call.Arguments, call.Err, m.width); status != "" {
				completionStatus = status
			}
		}
		if call.Err != nil {
			var correction correctionSuggester
			if errors.As(call.Err, &correction) && correction.CorrectionSuggested() {
				continue
			}
			if errors.Is(call.Err, errReviewUnavailable) {
				msg.runner.close()
				m.currentToolRegistry = nil
				return tea.Sequence(append(outputCmds, msgCmd(modsError{
					Err:        call.Err,
					ReasonText: "Tool execution requires review.",
				}))...)
			}
			continue
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
		return tea.Sequence(append(outputCmds, msgCmd(msg.runner.doneMsg()))...)
	}
	m.setActiveOperation(completionStatus)
	m.totalRounds++
	m.debugToolRounds++
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
		return tea.Sequence(append(outputCmds, msgCmd(msg.runner.doneMsg()))...)
	}
	debug.Print(DebugSection{Title: fmt.Sprintf("tool · turn %d · round %d · complete", m.debugTurn, m.debugRound), Fields: []DebugField{
		{Label: "calls", Value: fmt.Sprintf("%d", len(msg.results))},
		{Label: "budget", Value: fmt.Sprintf("total=%d/%d · failed=%d/%d", m.totalRounds, maxTotal, m.toolCallRounds, maxToolFailedRounds)},
	}})
	m.debugRound++
	m.debugStartModelRound()
	m.responseBoundaryPending = true
	return tea.Sequence(append(outputCmds, msg.runner.receiveCmd())...)
}
