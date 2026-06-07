package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/caarlos0/go-shellwords"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/mods/internal/anthropic"
	"github.com/charmbracelet/mods/internal/cache"
	"github.com/charmbracelet/mods/internal/cohere"
	"github.com/charmbracelet/mods/internal/google"
	"github.com/charmbracelet/mods/internal/ollama"
	"github.com/charmbracelet/mods/internal/openai"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
	"github.com/charmbracelet/mods/internal/websearch"
	"github.com/charmbracelet/x/exp/ordered"
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

// Mods is the Bubble Tea model that manages reading stdin and querying the
// OpenAI API.
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

	reviewChan    chan toolReviewItem
	reviewMode    ReviewMode
	approveAll    bool
	reviewPending bool
	reviewItem    *toolReviewItem

	harmlessCmds    map[string]bool
	harmlessGitCmds map[string]bool
	dangerousTokens []string
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
		Config:              cfg,
		reviewMode:          cfg.ReviewMode,
		harmlessCmds:        toBoolMap(cfg.Review.Shell.HarmlessCommands),
		harmlessGitCmds:     toBoolMap(cfg.Review.Shell.HarmlessGitCommands),
		dangerousTokens:     cfg.Review.Shell.DangerousPatterns,
		ctx:                 ctx,
	}
}

func toBoolMap(slice []string) map[string]bool {
	m := make(map[string]bool, len(slice))
	for _, s := range slice {
		m[s] = true
	}
	return m
}

// completionInput is a tea.Msg that wraps the content read from stdin.
type completionInput struct {
	content string
}

// completionOutput a tea.Msg that wraps the content returned from openai.
type completionOutput struct {
	content string
	stream  stream.Stream
	errh    func(error) tea.Msg
}

type toolCallsStartMsg struct {
	stream stream.Stream
	errh   func(error) tea.Msg
}

type toolCallsOutput struct {
	results []proto.ToolCallStatus
	stream  stream.Stream
	errh    func(error) tea.Msg
}

type toolOperationStatusMsg struct {
	content string
	done    bool
	ch      <-chan toolOperationStatusMsg
}

type toolReviewItem struct {
	name  string
	args  []byte
	label string
	resp  chan bool
}

type toolReviewStartMsg struct {
	item toolReviewItem
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
		reviewCh := make(chan toolReviewItem, 4)
		m.reviewChan = reviewCh
		cmds = append(cmds, m.pollToolOperationStatusCmd(ch), m.pollReviewCmd(reviewCh), m.callToolsCmd(msg, ch))
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
		m.reviewPending = true
		item := msg.item
		m.reviewItem = &item
		m.activeOperation = ""
	case toolCallsOutput:
		m.activeOperation = ""
		m.approveAll = false
		m.reviewChan = nil
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
		if m.reviewPending && m.reviewItem != nil {
			switch msg.String() {
			case "y", "Y":
				m.reviewItem.resp <- true
				m.reviewPending = false
				m.reviewItem = nil
				return m, m.pollReviewCmd(m.reviewChan)
			case "n", "N":
				m.reviewItem.resp <- false
				m.reviewPending = false
				m.reviewItem = nil
				return m, m.pollReviewCmd(m.reviewChan)
			case "a", "A":
				m.approveAll = true
				m.reviewItem.resp <- true
				m.reviewPending = false
				m.reviewItem = nil
				return m, m.pollReviewCmd(m.reviewChan)
			case "ctrl+c":
				m.reviewItem.resp <- false
				m.reviewPending = false
				m.reviewItem = nil
				return m, nil
			}
			return m, nil
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

func (m Mods) viewportNeeded() bool {
	return m.glamHeight > m.height
}

// View implements tea.Model.
func (m *Mods) View() string {
	//nolint:exhaustive
	switch m.state {
	case errorState:
		return ""
	case requestState:
		if !m.Config.Quiet {
			return m.renderWithOperation(m.anim.View())
		}
	case responseState:
		if !m.Config.Raw && isOutputTTY() {
			if m.viewportNeeded() {
				return m.renderWithOperation(m.glamViewport.View())
			}
			// We don't need the viewport yet.
			return m.renderWithOperation(m.glamOutput)
		}

		if isOutputTTY() && !m.Config.Raw {
			return m.renderWithOperation(m.Output)
		}

		m.contentMutex.Lock()
		for _, c := range m.content {
			fmt.Print(c)
		}
		m.content = []string{}
		m.contentMutex.Unlock()
	case doneState:
		if !isOutputTTY() {
			fmt.Printf("\n")
		}
		return ""
	}
	return ""
}

func (m *Mods) renderWithOperation(content string) string {
	if m.reviewPending && m.reviewItem != nil {
		return m.renderReviewBanner(content)
	}
	if m.Config.Quiet || !m.showOperationStatus {
		return content
	}
	if strings.TrimSpace(m.activeOperation) == "" && !m.reasoningActive {
		return content
	}
	status := m.operationStatusLine()
	if content == "" {
		return status
	}
	return strings.TrimRight(content, "\r\n") + "\n" + status
}

func (m *Mods) renderReviewBanner(content string) string {
	w := m.width
	if w <= 0 {
		w = 80
	}
	label := formatReviewLabel(m.reviewItem.name, m.reviewItem.args)
	promptLine := m.Styles.ReviewPrompt.Copy().Width(w).Render(
		padRight("  Review: "+label, w-4),
	)
	choicesLine := m.Styles.ReviewChoices.Copy().Width(w).Render(
		"  [Y] Approve  [N] Deny  [A] Approve All  [Ctrl+C] Cancel",
	)
	block := promptLine + "\n" + choicesLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
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

func padRight(s string, w int) string {
	s = strings.TrimSpace(s)
	runes := []rune(s)
	if len(runes) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(runes))
}

func (m *Mods) operationStatusLine() string {
	text := m.activeOperation
	if text == "" && m.reasoningActive {
		text = "Reasoning..."
	}
	if m.reasoningActive {
		badge := m.Styles.InlineCode.Render("[R]")
		return badge + " " + m.Styles.Comment.Render(truncateOperationStatus(text, m.width-4))
	}
	text = truncateOperationStatus(text, m.width)
	return m.Styles.Comment.Render(text)
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

func (m *Mods) retry(content string, err modsError) tea.Msg {
	m.retries++
	if m.retries >= m.Config.MaxRetries {
		debugPrintf("API error: giving up after %d retries (max=%d)", m.retries, m.Config.MaxRetries)
		return err
	}
	wait := time.Millisecond * 100 * time.Duration(math.Pow(2, float64(m.retries))) //nolint:mnd
	debugPrintf("API error: retry %d/%d in %v -> %s", m.retries, m.Config.MaxRetries, wait, err.reason)
	time.Sleep(wait)
	return completionInput{content}
}

func (m *Mods) startCompletionCmd(content string) tea.Cmd {
	if m.Config.Show != "" || m.Config.ShowLast {
		return m.readFromCache()
	}

	m.cancelRequest = nil
	m.reasoningActive = false

	return func() tea.Msg {
		var mod Model
		var api API
		var ccfg openai.Config
		var accfg anthropic.Config
		var cccfg cohere.Config
		var occfg ollama.Config
		var gccfg google.Config

		cfg := m.Config
		api, mod, err := m.resolveModel(cfg)
		cfg.API = mod.API
		if err != nil {
			return err
		}
		if api.Name == "" {
			eps := make([]string, 0)
			for _, a := range cfg.APIs {
				eps = append(eps, m.Styles.InlineCode.Render(a.Name))
			}
			return modsError{
				err: newUserErrorf(
					"Your configured API endpoints are: %s",
					eps,
				),
				reason: fmt.Sprintf(
					"The API endpoint %s is not configured.",
					m.Styles.InlineCode.Render(cfg.API),
				),
			}
		}

		switch mod.API {
		case "ollama":
			occfg = ollama.DefaultConfig()
			if api.BaseURL != "" {
				occfg.BaseURL = api.BaseURL
			}
		case "anthropic":
			key, err := m.ensureKey(api, "ANTHROPIC_API_KEY", "https://console.anthropic.com/settings/keys")
			if err != nil {
				return modsError{err, "Anthropic authentication failed"}
			}
			accfg = anthropic.DefaultConfig(key)
			if api.BaseURL != "" {
				accfg.BaseURL = api.BaseURL
			}
		case "google":
			key, err := m.ensureKey(api, "GOOGLE_API_KEY", "https://aistudio.google.com/app/apikey")
			if err != nil {
				return modsError{err, "Google authentication failed"}
			}
			gccfg = google.DefaultConfig(mod.Name, key)
			gccfg.ThinkingBudget = mod.ThinkingBudget
		case "cohere":
			key, err := m.ensureKey(api, "COHERE_API_KEY", "https://dashboard.cohere.com/api-keys")
			if err != nil {
				return modsError{err, "Cohere authentication failed"}
			}
			cccfg = cohere.DefaultConfig(key)
			if api.BaseURL != "" {
				ccfg.BaseURL = api.BaseURL
			}
		case "azure", "azure-ad": //nolint:goconst
			key, err := m.ensureKey(api, "AZURE_OPENAI_KEY", "https://aka.ms/oai/access")
			if err != nil {
				return modsError{err, "Azure authentication failed"}
			}
			ccfg = openai.Config{
				AuthToken: key,
				BaseURL:   api.BaseURL,
			}
			if mod.API == "azure-ad" {
				ccfg.APIType = "azure-ad"
			}
			if api.User != "" {
				cfg.User = api.User
			}
		default:
			key, err := m.ensureKey(api, "OPENAI_API_KEY", "https://platform.openai.com/account/api-keys")
			if err != nil {
				return modsError{err, "OpenAI authentication failed"}
			}
			ccfg = openai.Config{
				AuthToken: key,
				BaseURL:   api.BaseURL,
			}
		}

		m.reasoningActive = m.resolveReasoning(&mod, content, &accfg, &gccfg, &ccfg, occfg, cccfg)
		if m.reasoningActive {
			m.activeOperation = "Deep reasoning..."
		}

		if cfg.HTTPProxy != "" {
			proxyURL, err := url.Parse(cfg.HTTPProxy)
			if err != nil {
				return modsError{err, "There was an error parsing your proxy URL."}
			}
			httpClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
			ccfg.HTTPClient = httpClient
			accfg.HTTPClient = httpClient
			cccfg.HTTPClient = httpClient
			occfg.HTTPClient = httpClient
		}

		if mod.MaxChars == 0 {
			mod.MaxChars = cfg.MaxInputChars
		}

		// Check if the model is an o-series model and unset the max_tokens parameter
		// accordingly, as it's unsupported by o-series.
		if isOSeries(mod.Name) {
			cfg.MaxTokens = 0
		}

		wscfg := websearch.Config{
			Enabled:    cfg.WebSearch,
			Provider:   cfg.WebSearchProvider,
			APIKey:     cfg.WebSearchAPIKey,
			MaxResults: 5,
		}

		ctx, cancel := context.WithTimeout(m.ctx, config.MCPTimeout)
		m.cancelRequest = append(m.cancelRequest, cancel)
		registry, err := buildToolRegistry(ctx, cfg, wscfg, cfg.Prefix+"\n"+content)
		if err != nil {
			return err
		}
		tools := registry.Specs()

		if debugEnabled() {
			debugPrintf("Tools: %d total tool(s)", len(tools))
			for _, t := range tools {
				debugPrintf("  Tool: %s", t.Name)
			}
		}

		if err := m.setupStreamContext(content, mod); err != nil {
			return err
		}
		if mod.API == "cohere" {
			for _, msg := range m.messages {
				if len(msg.Images) > 0 {
					return modsError{
						err:    fmt.Errorf("image attachments are not supported for Cohere models"),
						reason: "Image attachments are not supported for this provider",
					}
				}
			}
		}

		request := proto.Request{
			Messages:    m.messages,
			API:         mod.API,
			Model:       mod.Name,
			User:        cfg.User,
			Temperature: ptrOrNil(cfg.Temperature),
			TopP:        ptrOrNil(cfg.TopP),
			TopK:        ptrOrNil(cfg.TopK),
			Stop:        cfg.Stop,
			Tools:       tools,
			ToolCaller: func(name string, data []byte) (string, error) {
				ctx, cancel := context.WithTimeout(m.ctx, config.MCPTimeout)
				m.cancelRequest = append(m.cancelRequest, cancel)
				m.sendToolOperationStatus(toolOperationLabel(name, data, m.width))

				if m.shouldReviewTool(name) && m.reviewChan != nil {
					if !m.requestApproval(name, data) {
						return "", fmt.Errorf("execution denied by user for: %s", name)
					}
				}

				return registry.Call(ctx, name, data)
			},
		}
		if cfg.MaxTokens > 0 {
			request.MaxTokens = &cfg.MaxTokens
		}

		var client stream.Client
		switch mod.API {
		case "anthropic":
			client = anthropic.New(accfg)
		case "google":
			client = google.New(gccfg)
		case "cohere":
			client = cohere.New(cccfg)
		case "ollama":
			client, err = ollama.New(occfg)
		default:
			client = openai.New(ccfg)
			if cfg.Format && config.FormatAs == "json" {
				request.ResponseFormat = &config.FormatAs
			}
		}
		if err != nil {
			return modsError{err, "Could not setup client"}
		}

		if debugEnabled() {
			debugPrintf("API request -> model=%s, api=%s", mod.Name, mod.API)
			debugPrintf("Request: %d message(s), %d tool definition(s)", len(m.messages), countTools(tools))
			tempStr := "unset"
			if request.Temperature != nil {
				tempStr = fmt.Sprintf("%.2f", *request.Temperature)
			}
			toppStr := "unset"
			if request.TopP != nil {
				toppStr = fmt.Sprintf("%.2f", *request.TopP)
			}
			maxTokStr := "unset"
			if request.MaxTokens != nil {
				maxTokStr = fmt.Sprintf("%d", *request.MaxTokens)
			}
			debugPrintf("Request: temp=%s, topp=%s, max_tokens=%s", tempStr, toppStr, maxTokStr)
			debugPrintf("Request: no-limit=%v, max-input-chars=%d", cfg.NoLimit, mod.MaxChars)
			if cfg.HTTPProxy != "" {
				debugPrintf("HTTP proxy: %s", cfg.HTTPProxy)
			}
			if b, err := json.Marshal(request.Messages); err == nil {
				toolBody, _ := json.Marshal(request.Tools)
				debugPrintf("Request: ~%dKB body (%dKB messages + %dKB tools)",
					(len(b)+len(toolBody))/1024, len(b)/1024, len(toolBody)/1024)
			}
		}

		stream := client.Request(m.ctx, request)
		return m.receiveCompletionStreamCmd(completionOutput{
			stream: stream,
			errh: func(err error) tea.Msg {
				return m.handleRequestError(err, mod, m.Input)
			},
		})()
	}
}

func (m Mods) ensureKey(api API, defaultEnv, docsURL string) (string, error) {
	key := api.APIKey
	if key == "" && api.APIKeyEnv != "" && api.APIKeyCmd == "" {
		key = os.Getenv(api.APIKeyEnv)
	}
	if key == "" && api.APIKeyCmd != "" {
		args, err := shellwords.Parse(api.APIKeyCmd)
		if err != nil {
			return "", modsError{err, "Failed to parse api-key-cmd"}
		}
		cmd := exec.Command(args[0], args[1:]...) //nolint:gosec
		hideCommandWindow(cmd)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return "", modsError{err, "Cannot exec api-key-cmd"}
		}
		key = strings.TrimSpace(string(out))
	}
	if key == "" {
		key = os.Getenv(defaultEnv)
	}
	if key != "" {
		return key, nil
	}
	return "", modsError{
		reason: fmt.Sprintf(
			"%[1]s required; set the environment variable %[1]s or update %[2]s through %[3]s.",
			m.Styles.InlineCode.Render(defaultEnv),
			m.Styles.InlineCode.Render("mods.yaml"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
		err: newUserErrorf(
			"You can grab one at %s",
			m.Styles.Link.Render(docsURL),
		),
	}
}

func (m *Mods) receiveCompletionStreamCmd(msg completionOutput) tea.Cmd {
	return func() tea.Msg {
		if msg.stream.Next() {
			chunk, err := msg.stream.Current()
			if err != nil && !errors.Is(err, stream.ErrNoContent) {
				_ = msg.stream.Close()
				return msg.errh(err)
			}
			if chunk.Thought != "" {
				debugPrintf("Thought: %s", chunk.Thought)
			}
			return completionOutput{
				content: chunk.Content,
				stream:  msg.stream,
				errh:    msg.errh,
			}
		}

		// stream is done, check for errors
		if err := msg.stream.Err(); err != nil {
			return msg.errh(err)
		}

		return toolCallsStartMsg{
			stream: msg.stream,
			errh:   msg.errh,
		}
	}
}

func (m *Mods) callToolsCmd(msg toolCallsStartMsg, ch chan toolOperationStatusMsg) tea.Cmd {
	return func() tea.Msg {
		results := msg.stream.CallTools()
		m.clearToolOperationChannel(ch)
		return toolCallsOutput{
			results: results,
			stream:  msg.stream,
			errh:    msg.errh,
		}
	}
}

func (m *Mods) pollToolOperationStatusCmd(ch <-chan toolOperationStatusMsg) tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return toolOperationStatusMsg{done: true}
		}
		msg.ch = ch
		return msg
	}
}

func (m *Mods) pollReviewCmd(ch <-chan toolReviewItem) tea.Cmd {
	return func() tea.Msg {
		item, ok := <-ch
		if !ok {
			return nil
		}
		return toolReviewStartMsg{item: item}
	}
}

func (m *Mods) shouldReviewTool(name string) bool {
	switch m.reviewMode {
	case ReviewNever:
		return false
	case ReviewAlways:
		return true
	default:
		return isMutableTool(name)
	}
}

func (m *Mods) requestApproval(name string, data []byte) bool {
	if m.approveAll {
		return true
	}
	if name == "shell_run" && m.isHarmlessShellCommand(extractShellCommand(data)) {
		return true
	}
	respCh := make(chan bool, 1)
	item := toolReviewItem{
		name:  name,
		args:  data,
		label: toolOperationLabel(name, data, m.width),
		resp:  respCh,
	}
	select {
	case m.reviewChan <- item:
	case <-m.ctx.Done():
		return false
	}
	select {
	case approved := <-respCh:
		return approved
	case <-m.ctx.Done():
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

func extractShellCommand(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

func (m *Mods) isHarmlessShellCommand(cmd string) bool {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return false
	}
	lower := strings.ToLower(cmd)

	if m.hasDangerousShellPattern(lower) {
		return false
	}

	return m.hasHarmlessBaseCommand(lower)
}

func (m *Mods) hasHarmlessBaseCommand(cmd string) bool {
	tokens := strings.Fields(cmd)
	if len(tokens) == 0 {
		return false
	}

	switch tokens[0] {
	case "git":
		if len(tokens) > 1 {
			return m.harmlessGitCmds[tokens[1]]
		}
		return false
	case "go":
		if len(tokens) > 1 {
			switch tokens[1] {
			case "doc", "env", "list", "version", "vet":
				return true
			}
		}
		return false
	default:
		return m.harmlessCmds[tokens[0]]
	}
}

func (m *Mods) hasDangerousShellPattern(cmd string) bool {
	for _, tok := range m.dangerousTokens {
		if strings.Contains(cmd, tok) {
			return true
		}
	}
	return false
}

func (m *Mods) setToolOperationChannel(ch chan<- toolOperationStatusMsg) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	m.toolOperations = ch
}

func (m *Mods) clearToolOperationChannel(ch chan toolOperationStatusMsg) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	if m.toolOperations != ch {
		return
	}
	close(ch)
	m.toolOperations = nil
}

func (m *Mods) sendToolOperationStatus(content string) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	if m.toolOperations == nil {
		return
	}
	select {
	case m.toolOperations <- toolOperationStatusMsg{content: content}:
	default:
	}
}

type cacheDetailsMsg struct {
	WriteID, Title, ReadID, API, Model string
}

func (m *Mods) findCacheOpsDetails() tea.Cmd {
	return func() tea.Msg {
		continueLast := m.Config.ContinueLast || (m.Config.Continue != "" && m.Config.Title == "")
		readID := ordered.First(m.Config.Continue, m.Config.Show)
		writeID := ordered.First(m.Config.Title, m.Config.Continue)
		title := writeID
		model := m.Config.Model
		api := m.Config.API

		if readID != "" || continueLast || m.Config.ShowLast {
			found, err := m.findReadID(readID)
			if err != nil {
				return modsError{
					err:    err,
					reason: "Could not find the conversation.",
				}
			}
			if found != nil {
				readID = found.ID
				if found.Model != nil && found.API != nil {
					model = *found.Model
					api = *found.API
				}
			}
		}

		if continueLast {
			writeID = readID
		}

		if writeID == "" {
			writeID = newConversationID()
		}

		if !sha1reg.MatchString(writeID) {
			convo, err := m.db.Find(writeID)
			if err != nil {
				writeID = newConversationID()
			} else {
				writeID = convo.ID
			}
		}

		debugPrintf("Conversation: write_id=%s, read_id=%s, continue_last=%v, title=%s",
			writeID[:min(sha1short, len(writeID))], readID[:min(sha1short, len(readID))], continueLast, title)

		return cacheDetailsMsg{
			WriteID: writeID,
			Title:   title,
			ReadID:  readID,
			API:     api,
			Model:   model,
		}
	}
}

func (m *Mods) findReadID(in string) (*Conversation, error) {
	convo, err := m.db.Find(in)
	if err == nil {
		return convo, nil
	}
	if errors.Is(err, errNoMatches) && m.Config.Show == "" {
		convo, err := m.db.FindHEAD()
		if err != nil {
			return nil, err
		}
		return convo, nil
	}
	return nil, err
}

func (m *Mods) readStdinCmd() tea.Msg {
	if !isInputTTY() {
		reader := bufio.NewReader(os.Stdin)
		stdinBytes, err := io.ReadAll(reader)
		if err != nil {
			return modsError{err, "Unable to read stdin."}
		}

		debugPrintf("Stdin: pipe mode, %d bytes read", len(stdinBytes))
		debugPrintf("Stdin image mode: %v", m.Config.StdinImage)

		if m.Config.StdinImage {
			return stdinImageInput{data: stdinBytes}
		}
		return completionInput{increaseIndent(string(stdinBytes))}
	}
	debugPrintf("Stdin: TTY mode, no piped input")
	return completionInput{""}
}

type stdinImageInput struct {
	data []byte
}

func (m *Mods) readFromCache() tea.Cmd {
	return func() tea.Msg {
		var messages []proto.Message
		if err := m.cache.Read(m.Config.cacheReadFromID, &messages); err != nil {
			return modsError{err, "There was an error loading the conversation."}
		}

		m.appendToOutput(proto.Conversation(messages).String())
		return completionOutput{
			errh: func(err error) tea.Msg {
				return modsError{err: err}
			},
		}
	}
}

const tabWidth = 4

func (m *Mods) appendToOutput(s string) {
	m.Output += s
	if !isOutputTTY() || m.Config.Raw {
		m.contentMutex.Lock()
		m.content = append(m.content, s)
		m.contentMutex.Unlock()
		return
	}

	wasAtBottom := m.glamViewport.ScrollPercent() == 1.0
	oldHeight := m.glamHeight
	m.glamOutput, _ = m.glam.Render(m.Output)
	m.glamOutput = strings.TrimRightFunc(m.glamOutput, unicode.IsSpace)
	m.glamOutput = strings.ReplaceAll(m.glamOutput, "\t", strings.Repeat(" ", tabWidth))
	m.glamHeight = lipgloss.Height(m.glamOutput)
	m.glamOutput += "\n"
	truncatedGlamOutput := m.renderer.NewStyle().
		MaxWidth(m.width).
		Render(m.glamOutput)
	m.glamViewport.SetContent(truncatedGlamOutput)
	if oldHeight < m.glamHeight && wasAtBottom {
		// If the viewport's at the bottom and we've received a new
		// line of content, follow the output by auto scrolling to
		// the bottom.
		m.glamViewport.GotoBottom()
	}
}

func toolOperationLabel(name string, data []byte, width int) string {
	args := toolOperationArgs(data)
	switch name {
	case "web_search":
		if query := oneLinePreview(argString(args, "query")); query != "" {
			return truncateOperationStatus("Searching web: "+query, width)
		}
	case "shell_run":
		if command := oneLinePreview(argString(args, "command")); command != "" {
			return truncateOperationStatus("Running command: "+command, width)
		}
	case "fs_read_file":
		if path := oneLinePreview(argString(args, "path")); path != "" {
			return truncateOperationStatus("Reading file: "+path, width)
		}
	case "fs_write_file":
		if path := oneLinePreview(argString(args, "path")); path != "" {
			return truncateOperationStatus("Writing file: "+path, width)
		}
	case "fs_list_dir":
		if path := oneLinePreview(argString(args, "path")); path != "" {
			return truncateOperationStatus("Listing directory: "+path, width)
		}
	case "fs_stat":
		if path := oneLinePreview(argString(args, "path")); path != "" {
			return truncateOperationStatus("Inspecting path: "+path, width)
		}
	case "fs_search":
		query := oneLinePreview(argString(args, "query"))
		path := oneLinePreview(argString(args, "path"))
		switch {
		case query != "" && path != "":
			return truncateOperationStatus("Searching files: "+query+" in "+path, width)
		case query != "":
			return truncateOperationStatus("Searching files: "+query, width)
		case path != "":
			return truncateOperationStatus("Searching files in: "+path, width)
		}
	case "fs_apply_patch":
		return truncateOperationStatus("Applying patch", width)
	case "thinking_note":
		if thought := oneLinePreview(argString(args, "thought")); thought != "" {
			return truncateOperationStatus("Thinking: "+thought, width)
		}
		return truncateOperationStatus("Thinking note", width)
	}
	if summary := toolArgsSummary(args); summary != "" {
		return truncateOperationStatus("Running tool: "+name+" ("+summary+")", width)
	}
	return truncateOperationStatus("Running tool: "+name, width)
}

func toolOperationArgs(data []byte) map[string]any {
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		return nil
	}
	return args
}

func toolArgsSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	preferred := []string{"query", "command", "path", "url", "repo", "repository", "file", "filename", "name"}
	parts := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, key := range preferred {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			return strings.Join(parts, ", ")
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	for key := range args {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func appendToolArgSummaryPart(parts *[]string, seen map[string]bool, args map[string]any, key string) bool {
	if seen[key] {
		return false
	}
	value := oneLinePreview(argString(args, key))
	if value == "" {
		return false
	}
	seen[key] = true
	*parts = append(*parts, key+"="+value)
	return true
}

func argString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			s := oneLinePreview(fmt.Sprint(item))
			if s != "" {
				values = append(values, s)
			}
		}
		return strings.Join(values, ",")
	default:
		return ""
	}
}

func oneLinePreview(s string) string {
	s = strings.ReplaceAll(s, "\r", "\n")
	s = firstLine(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}

func truncateOperationStatus(s string, width int) string {
	s = strings.TrimSpace(s)
	if width <= 0 || width > 120 {
		width = 120
	}
	runes := []rune(s)
	if len(runes) <= width {
		return s
	}
	if width <= 3 {
		return string(runes[:width])
	}
	return string(runes[:width-3]) + "..."
}

// if the input is whitespace only, make it empty.
func removeWhitespace(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

var tokenErrRe = regexp.MustCompile(`This model's maximum context length is (\d+) tokens. However, your messages resulted in (\d+) tokens`)

func cutPrompt(msg, prompt string) string {
	found := tokenErrRe.FindStringSubmatch(msg)
	if len(found) != 3 { //nolint:mnd
		return prompt
	}

	maxt, _ := strconv.Atoi(found[1])
	current, _ := strconv.Atoi(found[2])

	if maxt > current {
		return prompt
	}

	// 1 token =~ 4 chars
	// cut 10 extra chars 'just in case'
	reduceBy := 10 + (current-maxt)*4 //nolint:mnd
	if len(prompt) > reduceBy {
		return prompt[:len(prompt)-reduceBy]
	}

	return prompt
}

func increaseIndent(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = "\t" + lines[i]
	}
	return strings.Join(lines, "\n")
}

func (m *Mods) resolveReasoning(
	mod *Model,
	content string,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
	occfg ollama.Config,
	cccfg cohere.Config,
) bool {
	cfg := m.Config
	switch cfg.Reasoning {
	case ReasoningOn:
		applyReasoningConfigs(mod.API, gccfg, accfg, ccfg)
		debugPrintf("Reasoning: enabled for %s/%s", mod.API, mod.Name)
		return true
	case ReasoningAuto:
		if content == "" {
			return false
		}
		if mod.API == "cohere" || mod.API == "ollama" {
			return false
		}
		debugPrintf("Auto judge: evaluating task complexity for model=%s", mod.Name)
		m.activeOperation = "Evaluating task complexity..."
		judgeCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		// Reset reasoning configs for the judge call
		gccfgJ := *gccfg
		gccfgJ.ThinkingBudget = 0
		accfgJ := *accfg
		accfgJ.ThinkingBudget = 0
		ccfgJ := *ccfg
		ccfgJ.ReasoningEffort = ""
		shouldReason := judgeTaskComplexity(judgeCtx, mod, content, accfgJ, gccfgJ, ccfgJ, occfg, cccfg)
		debugPrintf("Auto judge: reasoning=%v", shouldReason)
		if shouldReason {
			applyReasoningConfigs(mod.API, gccfg, accfg, ccfg)
		}
		return shouldReason
	default:
		return false
	}
}

func applyReasoningConfigs(api string, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config) {
	switch {
	case api == "google":
		if gccfg.ThinkingBudget == 0 {
			gccfg.ThinkingBudget = 8192
		}
		debugPrintf("Reasoning: google thinking_budget=%d", gccfg.ThinkingBudget)
	case api == "anthropic":
		accfg.ThinkingBudget = 8192
		debugPrintf("Reasoning: anthropic thinking_budget=%d", accfg.ThinkingBudget)
	case api == "cohere" || api == "ollama":
		debugPrintf("Reasoning: %s does not support reasoning, skipped", api)
	default:
		ccfg.ReasoningEffort = openai.ReasoningEffortMedium
		debugPrintf("Reasoning: openai reasoning_effort=%s", ccfg.ReasoningEffort)
	}
}

func judgeTaskComplexity(
	ctx context.Context,
	mod *Model,
	prompt string,
	accfg anthropic.Config,
	gccfg google.Config,
	ccfg openai.Config,
	occfg ollama.Config,
	cccfg cohere.Config,
) bool {
	system := "You are a task classifier. Determine if the following task requires deep reasoning (multi-step analysis, debugging, complex logic, math, code design, or creative writing). Answer only YES."
	max3 := int64(3)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: prompt},
		},
		Model:       mod.Name,
		MaxTokens:   &max3,
		Temperature: ptrOrNil(float64(0)),
	}

	var client stream.Client
	var err error
	switch mod.API {
	case "anthropic":
		client = anthropic.New(accfg)
	case "google":
		client = google.New(gccfg)
	case "cohere":
		client = cohere.New(cccfg)
	case "ollama":
		client, err = ollama.New(occfg)
	default:
		client = openai.New(ccfg)
	}
	if err != nil {
		return false
	}

	st := client.Request(ctx, request)
	defer st.Close()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return false
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return false
	}
	return strings.Contains(strings.ToUpper(strings.TrimSpace(sb.String())), "YES")
}

func (m *Mods) resolveModel(cfg *Config) (API, Model, error) {
	for _, api := range cfg.APIs {
		if api.Name != cfg.API && cfg.API != "" {
			continue
		}
		for name, mod := range api.Models {
			if name == cfg.Model || slices.Contains(mod.Aliases, cfg.Model) {
				cfg.Model = name
				break
			}
		}
		mod, ok := api.Models[cfg.Model]
		if ok {
			mod.Name = cfg.Model
			mod.API = api.Name
			return api, mod, nil
		}
		if cfg.API != "" {
			return API{}, Model{}, modsError{
				err: newUserErrorf(
					"Available models are: %s",
					strings.Join(slices.Collect(maps.Keys(api.Models)), ", "),
				),
				reason: fmt.Sprintf(
					"The API endpoint %s does not contain the model %s",
					m.Styles.InlineCode.Render(cfg.API),
					m.Styles.InlineCode.Render(cfg.Model),
				),
			}
		}
	}

	return API{}, Model{}, modsError{
		reason: fmt.Sprintf(
			"Model %s is not in the settings file.",
			m.Styles.InlineCode.Render(cfg.Model),
		),
		err: newUserErrorf(
			"Please specify an API endpoint with %s or configure the model in the settings: %s",
			m.Styles.InlineCode.Render("--api"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
	}
}

type number interface{ int64 | float64 }

func isOSeries(model string) bool {
	prefixes := []string{"o1", "o3", "o4", "o5"}
	for _, p := range prefixes {
		if strings.HasPrefix(model, p) {
			return true
		}
	}
	return false
}

func ptrOrNil[T number](t T) *T {
	if t < 0 {
		return nil
	}
	return &t
}
