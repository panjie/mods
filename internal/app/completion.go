package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/caarlos0/go-shellwords"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
	toolregistry "github.com/charmbracelet/mods/internal/tools"
	"github.com/charmbracelet/mods/internal/websearch"
)

type number interface{ int64 | float64 }

func (m *Mods) retry(content string, err modsError) tea.Msg {
	m.retries++
	if m.retries >= m.Config.MaxRetries {
		debug.Printf("API error: giving up after %d retries (max=%d)", m.retries, m.Config.MaxRetries)
		return err
	}
	wait := time.Millisecond * 100 * time.Duration(math.Pow(2, float64(m.retries))) //nolint:mnd
	debug.Printf("API error: retry %d/%d in %v -> %s", m.retries, m.Config.MaxRetries, wait, err.ReasonText)
	time.Sleep(wait)
	return completionInput{content}
}

func (m *Mods) startCompletionCmd(content string) tea.Cmd {
	if m.Config.Show != "" || m.Config.ShowLast {
		return m.readFromCache()
	}

	m.cancelMu.Lock()
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	m.reasoningActive = false
	m.responseOutputStarted = false

	return func() tea.Msg {
		var mod Model
		var api API
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
				Err: newUserErrorf(
					"Your configured API endpoints are: %s",
					eps,
				),
				ReasonText: fmt.Sprintf(
					"The API endpoint %s is not configured.",
					m.Styles.InlineCode.Render(cfg.API),
				),
			}
		}

		cfgs, err := m.buildProviderConfigs(mod, api)
		if err != nil {
			return err
		}
		accfg := cfgs.Anthropic
		gccfg := cfgs.Google
		cccfg := cfgs.Cohere
		occfg := cfgs.Ollama
		ccfg := cfgs.OpenAI

		if (mod.API == "azure" || mod.API == "azure-ad") && api.User != "" {
			cfg.User = api.User
		}

		m.reasoningActive = m.resolveReasoning(&mod, content, &accfg, &gccfg, &ccfg, occfg, cccfg)
		if m.reasoningActive {
			m.setActiveOperation("Deep reasoning...")
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

		ctx, cancel := context.WithTimeout(m.ctx, cfg.MCPTimeout)
		m.addCancel(cancel)
		registry, err := BuildRegistry(ctx, cfg, wscfg, cfg.Prefix+"\n"+content)
		if err != nil {
			return err
		}
		tools := registry.Specs()

		if debug.Enabled() {
			debug.Printf("Tools: %d total tool(s)", len(tools))
			for _, t := range tools {
				debug.Printf("  Tool: %s", t.Name)
			}
		}

		if err := m.setupStreamContext(content, mod); err != nil {
			return err
		}

		if m.planContent != "" {
			planMsg := proto.Message{
				Role:    proto.RoleSystem,
				Content: "The user has approved the following plan for execution:\n\n" + m.planContent,
			}
			if len(m.messages) > 0 {
				last := m.messages[len(m.messages)-1]
				m.messages = append(m.messages[:len(m.messages)-1], planMsg, last)
			} else {
				m.messages = append(m.messages, planMsg)
			}
			m.planContent = ""
		}

		if mod.API == "cohere" {
			for _, msg := range m.messages {
				if len(msg.Images) > 0 {
					return modsError{
						Err:        fmt.Errorf("image attachments are not supported for Cohere models"),
						ReasonText: "Image attachments are not supported for this provider",
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
				ctx, cancel := m.toolCallContext(registry, name, cfg)
				m.addCancel(cancel)
				defer cancel()
				m.sendToolOperationStatus(ToolOperationLabel(name, data, m.width))

				if m.reviewer.shouldReviewTool(name) {
					if err := m.reviewer.requestApproval(m, name, data); err != nil {
						return "", err
					}
				}

				return registry.Call(ctx, name, data)
			},
		}
		if cfg.MaxTokens > 0 {
			request.MaxTokens = &cfg.MaxTokens
		}

		client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
		if err != nil {
			return modsError{err, "Could not setup client"}
		}
		if mod.API != "anthropic" && mod.API != "google" && mod.API != "cohere" && mod.API != "ollama" {
			if cfg.Format && cfg.FormatAs == "json" {
				request.ResponseFormat = &cfg.FormatAs
			}
		}

		debugRequest(cfg, &mod, &m.messages, tools, &request)

		stream := client.Request(m.ctx, request)
		return m.receiveCompletionStreamCmd(completionOutput{
			stream: stream,
			errh: func(err error) tea.Msg {
				return m.handleRequestError(err, mod, m.Input)
			},
		})()
	}
}

func (m *Mods) toolCallContext(registry *toolregistry.Registry, name string, cfg *Config) (context.Context, context.CancelFunc) {
	if registry.TimeoutPolicy(name) == toolregistry.TimeoutPolicySelf {
		return context.WithCancel(m.ctx)
	}
	return context.WithTimeout(m.ctx, cfg.MCPTimeout)
}

func (m *Mods) ensureKey(api API, defaultEnv, docsURL string) (string, error) {
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
		HideCommandWindow(cmd)
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
		ReasonText: fmt.Sprintf(
			"%[1]s required; set the environment variable %[1]s or update %[2]s through %[3]s.",
			m.Styles.InlineCode.Render(defaultEnv),
			m.Styles.InlineCode.Render("mods.yaml"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
		Err: newUserErrorf(
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
				debug.Printf("Thought: %s", chunk.Thought)
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

		msg.stream.Close()

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

func (m *Mods) setActiveOperation(op string) {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	m.activeOperation = op
}

func (m *Mods) getActiveOperation() string {
	m.operationMutex.Lock()
	defer m.operationMutex.Unlock()
	return m.activeOperation
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
				Err: newUserErrorf(
					"Available models are: %s",
					strings.Join(slices.Collect(maps.Keys(api.Models)), ", "),
				),
				ReasonText: fmt.Sprintf(
					"The API endpoint %s does not contain the model %s",
					m.Styles.InlineCode.Render(cfg.API),
					m.Styles.InlineCode.Render(cfg.Model),
				),
			}
		}
	}

	return API{}, Model{}, modsError{
		ReasonText: fmt.Sprintf(
			"Model %s is not in the settings file.",
			m.Styles.InlineCode.Render(cfg.Model),
		),
		Err: newUserErrorf(
			"Please specify an API endpoint with %s or configure the model in the settings: %s",
			m.Styles.InlineCode.Render("--api"),
			m.Styles.InlineCode.Render("mods --settings"),
		),
	}
}

// oSeriesRe matches OpenAI o-series model names: o + single digit + hyphen or end of string.
// Examples: "o1", "o1-mini", "o3-2025-04-16". Does NOT match "o10-" or custom names like "o1-finetune-v2".
var oSeriesRe = regexp.MustCompile(`^o[1-9](-|$)`)

func isOSeries(model string) bool {
	return oSeriesRe.MatchString(model)
}

func debugRequest(cfg *Config, mod *Model, messages *[]proto.Message, tools []proto.ToolSpec, request *proto.Request) {
	if !debug.Enabled() {
		return
	}
	debug.Printf("API request -> model=%s, api=%s", mod.Name, mod.API)
	debug.Printf("Request: %d message(s), %d tool definition(s)", len(*messages), len(tools))
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
	debug.Printf("Request: temp=%s, topp=%s, max_tokens=%s", tempStr, toppStr, maxTokStr)
	debug.Printf("Request: no-limit=%v, max-input-chars=%d", cfg.NoLimit, mod.MaxChars)
	if cfg.HTTPProxy != "" {
		debug.Printf("HTTP proxy: %s", cfg.HTTPProxy)
	}
	if b, err := json.Marshal(request.Messages); err == nil {
		toolBody, _ := json.Marshal(request.Tools)
		debug.Printf("Request: ~%dKB body (%dKB messages + %dKB tools)",
			(len(b)+len(toolBody))/1024, len(b)/1024, len(toolBody)/1024)
	}
}

func ptrOrNil[T number](t T) *T {
	if t < 0 {
		return nil
	}
	return &t
}
