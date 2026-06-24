package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/cohere"
	"github.com/panjie/mods/internal/google"
	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

type requestSession struct {
	stream  stream.Stream
	runner  *streamRunner
	cleanup *toolregistry.Registry
	errh    func(error) tea.Msg
}

func (m *Mods) buildRequestSession(content string) (requestSession, error) {
	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return requestSession{}, err
	}
	if api.Name == "" {
		return requestSession{}, m.apiNotConfiguredError(cfg, api)
	}

	if err := validateImagePaths(cfg.Images); err != nil {
		return requestSession{}, err
	}

	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return requestSession{}, err
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	cccfg := cfgs.Cohere
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI

	requestUser := cfg.User
	if (mod.API == "azure" || mod.API == "azure-ad") && api.User != "" {
		requestUser = api.User
	}

	reasoningActive := m.resolveReasoning(&mod, content, &accfg, &gccfg, &ccfg, occfg, cccfg)
	if err := applyHTTPProxy(cfg, &accfg, &gccfg, &cccfg, &occfg, &ccfg); err != nil {
		return requestSession{}, err
	}
	if mod.MaxChars == 0 {
		mod.MaxChars = cfg.MaxInputChars
	}
	maxTokens := cfg.MaxTokens
	if isOSeries(mod.Name) {
		maxTokens = 0
	}

	wscfg := websearch.Config{
		Enabled:    cfg.WebSearch,
		Provider:   cfg.WebSearchProvider,
		APIKey:     cfg.WebSearchAPIKey,
		MaxResults: 5,
	}

	registryCtx, cancel := context.WithTimeout(m.ctx, cfg.MCPTimeout)
	m.addCancel(cancel)
	registry, err := m.buildToolRegistryForProvider(registryCtx, cfg, wscfg, cfg.Prefix+"\n"+content, mod.API)
	if err != nil {
		cancel()
		return requestSession{}, err
	}
	m.currentToolRegistry = registry

	tools := registry.Specs()
	debugTools(tools, registry.Len())

	if cfg.Plan {
		err = m.setupPlanContext(content, mod)
	} else {
		err = m.setupStreamContext(content, mod)
	}
	if err != nil {
		_ = registry.Close()
		m.currentToolRegistry = nil
		return requestSession{}, err
	}
	if !cfg.Plan {
		m.injectApprovedPlan()
	}
	if err := rejectUnsupportedImages(mod.API, m.messages); err != nil {
		_ = registry.Close()
		m.currentToolRegistry = nil
		return requestSession{}, err
	}

	request := proto.Request{
		Messages:    m.messages,
		API:         mod.API,
		Model:       mod.Name,
		User:        requestUser,
		Temperature: ptrOrNil(cfg.Temperature),
		TopP:        ptrOrNil(cfg.TopP),
		TopK:        ptrOrNil(cfg.TopK),
		Stop:        cfg.Stop,
		Tools:       tools,
		ToolCaller:  m.toolCaller(registry, cfg),
	}
	if maxTokens > 0 {
		request.MaxTokens = &maxTokens
	}
	if supportsJSONResponseFormat(mod.API) && cfg.Format && cfg.FormatAs == "json" {
		request.ResponseFormat = &cfg.FormatAs
	}

	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		_ = registry.Close()
		m.currentToolRegistry = nil
		return requestSession{}, modsError{Err: err, ReasonText: "Could not setup client"}
	}

	debugRequest(cfg, &mod, &m.messages, tools, &request)
	m.reasoningActive = reasoningActive
	st := client.Request(m.ctx, request)
	errh := func(err error) tea.Msg {
		return m.handleRequestError(err, mod, m.Input)
	}
	return requestSession{
		stream:  st,
		runner:  newStreamRunner(st, registry, errh),
		cleanup: registry,
		errh:    errh,
	}, nil
}

func validateImagePaths(paths []string) error {
	for _, path := range paths {
		if _, _, err := imageutil.ReadImage(path); err != nil {
			return modsError{Err: err, ReasonText: "Could not read image file"}
		}
	}
	return nil
}

func (m *Mods) apiNotConfiguredError(cfg *Config, api API) modsError {
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

func applyHTTPProxy(cfg *Config, accfg *anthropic.Config, gccfg *google.Config, cccfg *cohere.Config, occfg *ollama.Config, ccfg *openai.Config) error {
	if cfg.HTTPProxy == "" {
		return nil
	}
	proxyURL, err := url.Parse(cfg.HTTPProxy)
	if err != nil {
		return modsError{Err: err, ReasonText: "There was an error parsing your proxy URL."}
	}
	httpClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	ccfg.HTTPClient = httpClient
	accfg.HTTPClient = httpClient
	cccfg.HTTPClient = httpClient
	occfg.HTTPClient = httpClient
	return nil
}

func debugTools(tools []proto.ToolSpec, total int) {
	if !debug.Enabled() {
		return
	}
	debug.Printf("Tools: %d total tool(s)", len(tools))
	for _, t := range tools {
		debug.Printf("  Tool: %s", t.Name)
	}
}

func (m *Mods) injectApprovedPlan() {
	if m.planContent == "" {
		return
	}
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

func rejectUnsupportedImages(api string, messages []proto.Message) error {
	if api != "cohere" {
		return nil
	}
	for _, msg := range messages {
		if len(msg.Images) > 0 {
			return modsError{
				Err:        fmt.Errorf("image attachments are not supported for Cohere models"),
				ReasonText: "Image attachments are not supported for this provider",
			}
		}
	}
	return nil
}

func supportsJSONResponseFormat(api string) bool {
	switch api {
	case "anthropic", "google", "cohere", "ollama":
		return false
	default:
		return true
	}
}

func (m *Mods) toolCaller(registry *toolregistry.Registry, cfg *Config) func(name string, data []byte) (string, error) {
	return func(name string, data []byte) (string, error) {
		ctx, cancel := m.toolCallContext(registry, name, cfg)
		m.addCancel(cancel)
		defer cancel()
		m.sendToolOperationStatus(ToolOperationLabel(name, data, m.width))

		if m.reviewer.shouldReviewTool(registry, name) {
			if err := m.reviewer.requestApproval(m, name, data); err != nil {
				return "", err
			}
		}

		return registry.Call(ctx, name, data)
	}
}
