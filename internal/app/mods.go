package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
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
// LLM APIs (OpenAI, Anthropic, Google, Cohere, Ollama, and others).
type Mods struct {
	outputRenderer
	Input                   string
	Styles                  Styles
	Error                   *modsError
	state                   state
	retries                 int
	toolCallRounds          int
	totalRounds             int
	renderer                *lipgloss.Renderer
	glam                    *glamour.TermRenderer
	glamViewport            viewport.Model
	messages                []proto.Message
	cancelRequest           []context.CancelFunc
	cancelMu                sync.Mutex
	anim                    tea.Model
	activeOperation         string
	reasoningActive         bool
	responseOutputStarted   bool
	responseBoundaryPending bool
	width                   int
	height                  int
	showOperationStatus     bool
	Thought                 string
	thoughtFlushed          bool

	db     *DB
	Config *Config

	content             []string
	contentMutex        *sync.Mutex
	operationMutex      sync.Mutex
	toolOperations      chan<- toolOperationStatusMsg
	currentToolRegistry *toolregistry.Registry

	stdinImageData []byte

	ctx context.Context

	reviewer *toolReviewer

	shellAnalyzer func(tool, command string) shellCommandAnalysis

	planContent  string
	planRetries  int
	planSelected int

	proposals        []proposal
	proposalSelected int
	proposalMode     bool

	feedbackInput textinput.Model
	feedbackMode  bool
}

func New(
	ctx context.Context,
	r *lipgloss.Renderer,
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
	vp := viewport.New(0, 0)
	vp.GotoBottom()
	return &Mods{
		Styles:              MakeStyles(r),
		glam:                gr,
		state:               startState,
		renderer:            r,
		glamViewport:        vp,
		contentMutex:        &sync.Mutex{},
		showOperationStatus: IsOutputTTY() && IsErrorTTY() && !cfg.Raw && !cfg.HideToolStatus,
		db:                  db,
		Config:              cfg,
		reviewer:            newToolReviewer(cfg),
		ctx:                 ctx,
	}, nil
}

func (m *Mods) Err() *modsError {
	return m.Error
}

func (m *Mods) RenderedOutput() string {
	return m.glamOutput
}

func (m *Mods) Messages() []proto.Message {
	return append([]proto.Message(nil), m.messages...)
}

func (m *Mods) ApprovalRules() []Rule {
	return m.reviewer.rules.Snapshot()
}

// Init implements tea.Model.
func (m *Mods) Init() tea.Cmd {
	return m.findCacheOpsDetails()
}

// Update implements tea.Model.
func (m *Mods) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

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
	case cacheDetailsMsg:
		m.Config.CacheWriteToID = msg.WriteID
		m.Config.CacheWriteToTitle = msg.Title
		m.Config.CacheReadFromID = msg.ReadID
		m.Config.API = msg.API
		m.Config.Model = msg.Model
		m.reviewer.rules.Replace(msg.Rules)

		if !m.Config.Quiet {
			m.anim = NewAnim(defaultFanciness, m.Config.StatusText, m.renderer, m.Styles)
			cmds = append(cmds, m.anim.Init())
		}
		m.state = configLoadedState
		cmds = append(cmds, m.readStdinCmd)

	case stdinImageInput:
		m.stdinImageData = msg.data
		if m.Config.Prefix == "" && m.Config.Show == "" && !m.Config.ShowLast {
			return m, m.quit
		}
		if m.Config.Dirs ||
			len(m.Config.Delete) > 0 ||
			m.Config.DeleteOlderThan != 0 ||
			m.Config.ShowHelp ||
			m.Config.List ||
			m.Config.ListRoles ||
			m.Config.Settings ||
			m.Config.ConfigSetup ||
			m.Config.ResetSettings {
			return m, m.quit
		}
		m.state = requestState
		cmds = append(cmds, m.startCompletionCmd(""))

	case completionInput:
		if msg.content != "" {
			m.Input = RemoveWhitespace(msg.content)
		}
		if m.Input == "" && m.Config.Prefix == "" && m.Config.Show == "" && !m.Config.ShowLast {
			return m, m.quit
		}
		if m.Config.Dirs ||
			len(m.Config.Delete) > 0 ||
			m.Config.DeleteOlderThan != 0 ||
			m.Config.ShowHelp ||
			m.Config.List ||
			m.Config.ListRoles ||
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
				if m.reasoningActive {
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
	case planCompleteMsg:
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
		m.state = planState
		return m, m.startPlanCmd("The previous plan was rejected. Please create a completely different plan.")
	case planModifyMsg:
		m.clearProposals()
		m.resetOutputBuffers()
		m.planContent = ""
		m.planRetries = 0
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
		m.glamViewport.Width = m.width
		m.glamViewport.Height = m.height
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
	return !m.Config.Quiet && !debug.Enabled() &&
		m.anim != nil &&
		(m.state == configLoadedState ||
			(m.state == planState && m.planContent == "") ||
			m.state == requestState ||
			(m.state == responseState && !m.responseOutputStarted))
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

func (m *Mods) quit() tea.Msg {
	m.cancelMu.Lock()
	defer m.cancelMu.Unlock()
	for _, cancel := range m.cancelRequest {
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
	if m.reasoningActive && !m.thoughtFlushed {
		m.flushThought()
	} else if !m.reasoningActive && !m.thoughtFlushed && strings.TrimSpace(m.Thought) != "" {
		debug.Printf("Reasoning: model emitted %d chars of thinking without -T (discarded; pass -T to display)", len(strings.TrimSpace(m.Thought)))
		m.thoughtFlushed = true
	}
	m.appendToOutput(content)
	if m.Config.Plan {
		m.state = planState
	} else {
		m.state = responseState
	}
	return msg.runner.receiveCmd()
}

func (m *Mods) startToolCalls(runner *streamRunner) []tea.Cmd {
	// The model may reason and then immediately call a tool without emitting any
	// answer text; flush the thought so it is still shown.
	if m.reasoningActive && !m.thoughtFlushed {
		m.flushThought()
	} else if !m.reasoningActive && !m.thoughtFlushed && strings.TrimSpace(m.Thought) != "" {
		debug.Printf("Reasoning: model emitted %d chars of thinking without -T (discarded; pass -T to display)", len(strings.TrimSpace(m.Thought)))
		m.thoughtFlushed = true
	}
	ch := make(chan toolOperationStatusMsg, 8)
	m.setToolOperationChannel(ch)
	m.setActiveOperation("Running tools")
	m.state = responseState
	cmds := []tea.Cmd{m.pollToolOperationStatusCmd(ch), m.callToolsCmd(runner, ch)}
	cmds = append(cmds[:1], append([]tea.Cmd{m.reviewer.startSession()}, cmds[1:]...)...)
	return cmds
}

func (m *Mods) handleToolCallsDone(msg streamEventMsg) tea.Cmd {
	m.setActiveOperation("")
	m.reviewer.reset()
	for _, call := range msg.results {
		if call.Err != nil {
			debug.Printf("Tool call FAILED: %s -> %v", call.Name, call.Err)
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
		if argPreview != "" {
			debug.Printf("Tool call: %s(%s)", call.Name, argPreview)
		} else {
			debug.Printf("Tool call: %s", call.Name)
		}
	}
	if len(msg.results) == 0 {
		msg.runner.close()
		m.messages = msg.runner.messages()
		m.toolCallRounds = 0
		m.totalRounds = 0
		m.setActiveOperation(m.Config.StatusText)
		return msgCmd(msg.runner.doneMsg())
	}
	m.totalRounds++
	hasFailed := slices.ContainsFunc(msg.results, func(c proto.ToolCallStatus) bool {
		return c.Err != nil
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
		m.planSelected = 1
		return nil, true
	case "right":
		idx := m.proposalSelected + 1
		if idx >= len(m.proposals) {
			idx = 0
		}
		m.showProposal(idx)
		m.planSelected = 1
		return nil, true
	case "y", "Y":
		m.planContent = m.proposals[m.proposalSelected].content
		m.clearProposals()
		m.planSelected = 0
		return nil, true
	case "m", "M":
		ti := textinput.New()
		ti.Placeholder = "Describe changes you want to make to this proposal..."
		ti.Width = max(m.width-4, 20)
		m.feedbackInput = ti
		m.feedbackMode = true
		return m.feedbackInput.Focus(), true
	case "enter":
		switch m.planSelected {
		case 0:
			idx := m.proposalSelected - 1
			if idx < 0 {
				idx = len(m.proposals) - 1
			}
			m.showProposal(idx)
			m.planSelected = 1
			return nil, true
		case 1:
			m.planContent = m.proposals[m.proposalSelected].content
			m.clearProposals()
			m.planSelected = 0
			return nil, true
		case 2:
			ti := textinput.New()
			ti.Placeholder = "Describe changes you want to make to this proposal..."
			ti.Width = max(m.width-4, 20)
			m.feedbackInput = ti
			m.feedbackMode = true
			return m.feedbackInput.Focus(), true
		case 3:
			m.clearProposals()
			return msgCmd(planDeniedMsg{content: m.Config.Prefix}), true
		case 4:
			m.state = doneState
			return m.quit, true
		}
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
		ti.Width = max(m.width-4, 20)
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
			ti.Width = max(m.width-4, 20)
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
