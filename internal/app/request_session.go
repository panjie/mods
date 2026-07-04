package app

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"

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

	reasoningActive, err := m.resolveReasoning(&mod, &accfg, &gccfg, &ccfg)
	if err != nil {
		return requestSession{}, err
	}
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

	// Construct the provider client before building the tool registry so
	// the registry decision can consult client.Capabilities().Tools
	// instead of an app-layer string switch keyed on the API name. The
	// client has no side effects until Request is called, so creating it
	// here (rather than after the registry) does not change behavior.
	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		return requestSession{}, modsError{Err: err, ReasonText: "Could not setup client"}
	}

	registryCtx, cancel := context.WithTimeout(m.ctx, cfg.MCPTimeout)
	m.addCancel(cancel)
	registry, err := m.buildToolRegistryForProvider(registryCtx, cfg, wscfg, cfg.Prefix+"\n"+content, client)
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
		// Plan -> execution transition: snapshot the plan-phase conversation
		// before setupStreamContext resets m.messages, so the investigation
		// can be re-injected into the execution turn. Gated on planContent so
		// ordinary (non-plan) turns are unaffected.
		if m.planContent != "" {
			m.capturePlanHistory()
		}
		err = m.setupStreamContext(content, mod)
	}
	if err != nil {
		_ = registry.Close()
		m.currentToolRegistry = nil
		return requestSession{}, err
	}
	if !cfg.Plan {
		m.injectPlanHistory()
		m.injectApprovedPlan()
	}
	if err := rejectUnsupportedImages(mod.API, m.messages); err != nil {
		_ = registry.Close()
		m.currentToolRegistry = nil
		return requestSession{}, err
	}

	request := proto.Request{
		Messages:   m.messages,
		API:        mod.API,
		Model:      mod.Name,
		User:       requestUser,
		Tools:      tools,
		ToolCaller: m.toolCaller(registry, cfg),
	}
	if maxTokens > 0 {
		request.MaxTokens = &maxTokens
	}
	if supportsJSONResponseFormat(mod.API) && cfg.Format && cfg.FormatAs == "json" {
		request.ResponseFormat = &cfg.FormatAs
	}

	debugRequest(cfg, &mod, &m.messages, tools, &request)
	m.reasoningActive = reasoningActive
	// Derive a cancellable context for the stream so quit() / a subsequent
	// start*Cmd can tear down the in-flight HTTP/SSE request rather than
	// waiting for it to finish on its own. The cancel is owned by the
	// streamRunner and released by close().
	streamCtx, streamCancel := context.WithCancel(m.ctx)
	st := client.Request(streamCtx, request)
	errh := func(err error) tea.Msg {
		return m.handleRequestError(err, mod, m.Input)
	}
	runner := newStreamRunner(st, registry, streamCancel, errh)
	m.setActiveRunner(runner)
	return requestSession{
		stream:  st,
		runner:  runner,
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
	gccfg.HTTPClient = httpClient
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
	m.messages = append(m.messages, proto.Message{
		Role:    proto.RoleSystem,
		Content: formatApprovedPlanPrompt(m.planContent),
	})
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

		// Reject malformed calls (missing/empty required args) before computing
		// access intent or asking for approval. Without this, a call that omits
		// a required path renders a misleading "unknown target" review prompt
		// for an operation that would fail in Call anyway.
		if err := registry.ValidateRequiredArgs(name, data); err != nil {
			return "", err
		}

		intent := buildAccessIntent(name, data, registry, m.analyzeShellCommand)
		scope := m.reviewer.scope
		safeDirs := []string{os.TempDir()}
		if len(intent.Dirs) > 0 {
			if registry.ShellExecution(name) {
				intent.Dirs = normalizeShellAffectedDirsForTool(intent.Dirs, scope.Value, name)
			} else {
				intent.Dirs = normalizeAffectedDirsForWorkspace(intent.Dirs, scope.Value)
			}
		}

		// Inject authorized external directories so resolveWorkspacePath honors
		// approval. This applies whether or not review is skipped below: a
		// saved DirAllow rule may auto-approve the call, but the tool still
		// needs the authorization to touch the external path.
		if ext := ExternalDirs(intent, scope, safeDirs); len(ext) > 0 {
			ctx = toolregistry.WithAuthorizedDirs(ctx, ext)
		}

		// Mutable tools always go through review. Read-only tools
		// (shouldReviewTool=false) still require approval when the matrix says
		// the access is external, so an out-of-workspace read is not silently
		// allowed.
		needsReview := m.reviewer.shouldReviewTool(registry, name)
		if !needsReview && ClassifyAccess(intent, scope, safeDirs, ApprovalReviewMode(m.reviewer.reviewMode)) == DecisionAsk {
			needsReview = true
		}

		if needsReview {
			if err := m.reviewer.requestApproval(reviewerDeps{
				ctx:              m.ctx,
				isShellExecution: registry.ShellExecution,
				analyzeShell:     m.analyzeShellCommand,
				accessIntent:     intent,
			}, name, data); err != nil {
				return "", err
			}
		}

		return registry.Call(ctx, name, data)
	}
}
