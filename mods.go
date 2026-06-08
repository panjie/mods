package main

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/mods/internal/cache"
	"github.com/charmbracelet/mods/internal/proto"
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

// Mods is the Bubble Tea model that manages reading stdin and querying
// LLM APIs (OpenAI, Anthropic, Google, Cohere, Ollama, and others).
type Mods struct {
	Output              string
	Input               string
	Styles              styles
	Error               *modsError
	state               state
	retries             int
	toolCallRounds      int
	totalRounds         int
	renderer            *lipgloss.Renderer
	glam                *glamour.TermRenderer
	glamViewport        viewport.Model
	glamOutput          string
	glamHeight          int
	messages            []proto.Message
	cancelRequest       []context.CancelFunc
	anim                tea.Model
	activeOperation     string
	reasoningActive     bool
	width               int
	height              int
	showOperationStatus bool

	db     *convoDB
	cache  *cache.Conversations
	Config *Config

	content        []string
	contentMutex   *sync.Mutex
	operationMutex sync.Mutex
	toolOperations chan<- toolOperationStatusMsg

	stdinImageData []byte

	ctx context.Context

	reviewer *toolReviewer
}

func newMods(
	ctx context.Context,
	r *lipgloss.Renderer,
	cfg *Config,
	db *convoDB,
	cache *cache.Conversations,
) *Mods {
	gr, _ := glamour.NewTermRenderer(
		glamour.WithEnvironmentConfig(),
		glamour.WithWordWrap(cfg.WordWrap),
	)
	vp := viewport.New(0, 0)
	vp.GotoBottom()
	return &Mods{
		Styles:              makeStyles(r),
		glam:                gr,
		state:               startState,
		renderer:            r,
		glamViewport:        vp,
		contentMutex:        &sync.Mutex{},
		showOperationStatus: isOutputTTY() && isErrorTTY() && !cfg.Raw,
		db:                  db,
		cache:               cache,
		Config:   cfg,
		reviewer: newToolReviewer(cfg),
		ctx:      ctx,
	}
}

// Init implements tea.Model.
func (m *Mods) Init() tea.Cmd {
	return m.findCacheOpsDetails()
}

// Update implements tea.Model.
func (m *Mods) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case cacheDetailsMsg:
		m.Config.cacheWriteToID = msg.WriteID
		m.Config.cacheWriteToTitle = msg.Title
		m.Config.cacheReadFromID = msg.ReadID
		m.Config.API = msg.API
		m.Config.Model = msg.Model

		if !m.Config.Quiet {
			m.anim = newAnim(m.Config.Fanciness, m.Config.StatusText, m.renderer, m.Styles)
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
			m.Config.ResetSettings {
			return m, m.quit
		}
		m.state = requestState
		cmds = append(cmds, m.startCompletionCmd(""))

	case completionInput:
		if msg.content != "" {
			m.Input = removeWhitespace(msg.content)
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
			m.Config.ResetSettings {
			return m, m.quit
		}

		if m.Config.IncludePromptArgs {
			m.appendToOutput(m.Config.Prefix + "\n\n")
		}

		if m.Config.IncludePrompt > 0 {
			parts := strings.Split(strings.ReplaceAll(m.Input, "\r\n", "\n"), "\n")
			if len(parts) > m.Config.IncludePrompt {
				parts = parts[0:m.Config.IncludePrompt]
			}
			m.appendToOutput(strings.Join(parts, "\n") + "\n")
		}
		m.state = requestState
		cmds = append(cmds, m.startCompletionCmd(msg.content))
	case completionOutput:
		if msg.stream == nil {
			m.state = doneState
			return m, m.quit
		}
		if msg.content != "" {
			if m.reasoningActive {
				m.activeOperation = "Deep reasoning..."
			} else {
				m.activeOperation = ""
			}
			m.appendToOutput(msg.content)
			m.state = responseState
		}
		cmds = append(cmds, m.receiveCompletionStreamCmd(completionOutput{
			stream: msg.stream,
			errh:   msg.errh,
		}))
	case toolCallsStartMsg:
		ch := make(chan toolOperationStatusMsg, 8)
		m.setToolOperationChannel(ch)
		m.activeOperation = "Running tools"
		m.state = responseState
		cmds = append(cmds, m.pollToolOperationStatusCmd(ch), m.reviewer.startSession(), m.callToolsCmd(msg, ch))
	case toolOperationStatusMsg:
		if msg.done {
			m.activeOperation = ""
			break
		}
		m.activeOperation = msg.content
		if msg.ch != nil {
			cmds = append(cmds, m.pollToolOperationStatusCmd(msg.ch))
		}
	case toolReviewStartMsg:
		m.reviewer.handleStartMsg(msg)
		m.activeOperation = ""
	case toolCallsOutput:
		m.activeOperation = ""
		m.reviewer.reset()
		toolMsg := completionOutput{
			stream: msg.stream,
			errh:   msg.errh,
		}
		for _, call := range msg.results {
			if call.Err != nil {
				debugPrintf("Tool call FAILED: %s -> %v", call.Name, call.Err)
			} else {
				argPreview := truncateStr(string(call.Arguments), 120)
				if argPreview != "" {
					debugPrintf("Tool call: %s(%s)", call.Name, argPreview)
				} else {
					debugPrintf("Tool call: %s", call.Name)
				}
			}
		}
		if len(msg.results) == 0 {
			m.messages = msg.stream.Messages()
			m.toolCallRounds = 0
			m.totalRounds = 0
			m.activeOperation = m.Config.StatusText
			return m, msgCmd(completionOutput{errh: msg.errh})
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
			maxTotal = 30
		}
		const maxFailedRounds = 3
		if m.toolCallRounds > maxFailedRounds {
			debugPrintf("Tool call failed rounds exceeded limit (%d), stopping", maxFailedRounds)
			m.messages = msg.stream.Messages()
			content := lastAssistantContent(m.messages)
			if content != "" {
				m.appendToOutput(content)
			}
			return m, msgCmd(completionOutput{errh: msg.errh})
		}
		if m.totalRounds > maxTotal {
			debugPrintf("Tool call total rounds exceeded limit (%d), stopping", maxTotal)
			m.messages = msg.stream.Messages()
			content := lastAssistantContent(m.messages)
			if content != "" {
				m.appendToOutput(content)
			}
			return m, msgCmd(completionOutput{errh: msg.errh})
		}
		debugPrintf("Tool call round %d (total=%d/%d, failed=%d/%d)", m.toolCallRounds, m.totalRounds, maxTotal, m.toolCallRounds, maxFailedRounds)
		return m, msgCmd(toolMsg)
	case modsError:
		m.Error = &msg
		m.state = errorState
		return m, m.quit
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.glamViewport.Width = m.width
		m.glamViewport.Height = m.height
		return m, nil
	case tea.KeyMsg:
	if handled, cmd := m.reviewer.handleKey(msg); handled {
		return m, cmd
	}
	switch msg.String() {
		case "q", "ctrl+c":
			m.state = doneState
			return m, m.quit
		}
	}
	if !m.Config.Quiet && (m.state == configLoadedState || m.state == requestState) && m.anim != nil {
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

func (m *Mods) quit() tea.Msg {
	for _, cancel := range m.cancelRequest {
		cancel()
	}
	return tea.Quit()
}


