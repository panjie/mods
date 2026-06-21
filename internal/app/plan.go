package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/websearch"
)

const planSystemPrompt = `You are in PLAN mode. Before executing anything, you must first create a detailed, step-by-step plan for the user to review.

You have access to read-only tools (file reading, directory listing, searching, web search) to inspect the codebase. Use them freely to understand the code.

Think through the task carefully and include:
- What files need to be created or modified
- What commands need to be run
- What changes need to be made
- Any potential issues, edge cases, or risks
- The order in which steps should be executed

Output your plan in plain text. Do NOT request any tool execution during this phase.
The user will review your plan and approve or deny it before execution begins.`

const maxPlanRetries = 3

var planReadOnlyTools = map[string]bool{
	"fs_read_file":  true,
	"fs_list_dir":   true,
	"fs_stat":       true,
	"fs_search":     true,
	"web_search":    true,
	"thinking_note": true,
}

func (m *Mods) setupPlanContext(content string, mod Model) error {
	if err := m.setupStreamContext(content, mod); err != nil {
		return err
	}
	planMsg := proto.Message{
		Role:    proto.RoleSystem,
		Content: planSystemPrompt,
	}
	if len(m.messages) > 0 && m.messages[0].Role == proto.RoleSystem {
		m.messages = append(
			m.messages[:1],
			append([]proto.Message{planMsg}, m.messages[1:]...)...,
		)
	} else {
		m.messages = append([]proto.Message{planMsg}, m.messages...)
	}
	return nil
}

func (m *Mods) startPlanCmd(content string) tea.Cmd {
	m.cancelMu.Lock()
	m.cancelRequest = nil
	m.cancelMu.Unlock()
	m.responseOutputStarted = false
	m.Output = ""

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
				ReasonText: "The API endpoint " + m.Styles.InlineCode.Render(cfg.API) + " is not configured.",
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

		m.reasoningActive = m.resolveReasoning(&mod, content, &accfg, &gccfg, &ccfg, occfg, cccfg)

		if cfg.HTTPProxy != "" {
			proxyURL, err := url.Parse(cfg.HTTPProxy)
			if err != nil {
				return modsError{Err: err, ReasonText: "There was an error parsing your proxy URL."}
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
		registry, err := m.buildToolRegistryForProvider(ctx, cfg, wscfg, cfg.Prefix+"\n"+content, mod.API)
		if err != nil {
			return err
		}

		var tools []proto.ToolSpec
		for _, t := range registry.Specs() {
			if planReadOnlyTools[t.Name] {
				tools = append(tools, t)
			}
		}

		if debug.Enabled() {
			debug.Printf("Plan mode: %d read-only tool(s) of %d total", len(tools), registry.Len())
			for _, t := range tools {
				debug.Printf("  Plan tool: %s", t.Name)
			}
		}

		if err := m.setupPlanContext(content, mod); err != nil {
			return err
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
				if !planReadOnlyTools[name] {
					return "", fmt.Errorf("tool %q is not available during planning phase", name)
				}
				ctx, cancel := m.toolCallContext(registry, name, cfg)
				m.addCancel(cancel)
				defer cancel()
				m.sendToolOperationStatus(ToolOperationLabel(name, data, m.width))
				return registry.Call(ctx, name, data)
			},
		}
		if cfg.MaxTokens > 0 {
			request.MaxTokens = &cfg.MaxTokens
		}

		client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
		if err != nil {
			return modsError{Err: err, ReasonText: "Could not setup client"}
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

func (m *Mods) renderPlanReviewBanner(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	promptLine := m.Styles.ReviewPrompt.Copy().Width(m.width).Render(
		padRight("  Plan Review", m.width-4),
	)
	baseStyle := m.Styles.ReviewChoices.Copy().Padding(0, 0)
	selectedStyle := baseStyle.Copy().
		Foreground(lipgloss.Color("#4A3B9F")).
		Background(lipgloss.Color("#E0DDFF"))
	options := []string{
		"[Y] Approve",
		"[N] Try again",
		"[M] Modify",
		"[Ctrl+C] Cancel",
	}
	var parts []string
	for i, opt := range options {
		if i == m.planSelected {
			parts = append(parts, selectedStyle.Render(opt))
		} else {
			parts = append(parts, baseStyle.Render(opt))
		}
	}
	choicesLine := m.Styles.ReviewChoices.Copy().Width(m.width).Render(strings.Join(parts, "  "))
	block := promptLine + "\n" + choicesLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func (m *Mods) renderPlanFeedbackInput(content string) string {
	if m.width <= 0 {
		m.width = 80
	}
	promptLine := m.Styles.ReviewPrompt.Copy().Width(m.width).Render(
		padRight("  Modification Feedback", m.width-4),
	)
	inputLine := m.Styles.ReviewChoices.Copy().Width(m.width).Render(
		"  " + m.feedbackInput.View(),
	)
	block := promptLine + "\n" + inputLine
	if strings.TrimSpace(content) == "" {
		return block
	}
	return strings.TrimRight(content, "\r\n") + "\n" + block
}

func (m *Mods) approvedPlanTranscript() string {
	content := m.glamOutput
	if content == "" {
		content = m.Output
	}
	content = strings.TrimRight(content, "\r\n")
	if strings.TrimSpace(content) == "" {
		return ""
	}
	return content + "\n"
}

func (m *Mods) approvedPlanPrintCmd(transcript string) tea.Cmd {
	if transcript == "" || m.Config.Raw || !isOutputTTY() {
		return nil
	}
	return tea.Println(transcript)
}

func (m *Mods) resetExecutionOutput() {
	m.Output = ""
	m.glamOutput = ""
	m.glamHeight = 0
	m.glamViewport.SetContent("")
	m.responseOutputStarted = false
}
