package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/approval"
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
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI

	requestUser := cfg.User
	if modelProtocol(mod) == "azure" && api.User != "" {
		requestUser = api.User
	}

	thinkActive, err := m.resolveThinkWithOllama(&mod, &accfg, &gccfg, &occfg, &ccfg)
	if err != nil {
		return requestSession{}, err
	}
	if err := applyHTTPProxy(cfg, &accfg, &gccfg, &occfg, &ccfg); err != nil {
		return requestSession{}, err
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
	client, err := newStreamClient(modelProtocol(mod), accfg, gccfg, occfg, ccfg)
	if err != nil {
		return requestSession{}, modsError{Err: err, ReasonText: "Could not setup client"}
	}
	if len(cfg.Images) > 0 && !client.Capabilities().Images {
		return requestSession{}, modsError{
			Err:        fmt.Errorf("%s/%s does not accept image or file input on endpoint %s", mod.API, mod.Name, normalizedEndpointName(ccfg.UseResponses)),
			ReasonText: "The selected provider endpoint does not support image input",
		}
	}
	localWebSearch, providerWebSearch := resolveWebSearch(cfg, client.Capabilities())
	debug.Printf("Web search tools: local=%v provider_hosted=%v", localWebSearch, providerWebSearch)

	err = m.setupStreamContext(content)
	if err != nil {
		return requestSession{}, err
	}
	m.sanitizeProviderContinuation(mod, ccfg.UseResponses)

	registryCtx, cancel := context.WithTimeout(m.ctx, cfg.MCPTimeout)
	m.addCancel(cancel)
	toolCfg := *cfg
	toolCfg.WebSearch = localWebSearch
	wscfg.Enabled = localWebSearch
	registry, err := m.buildToolRegistryForProvider(
		registryCtx, &toolCfg, wscfg, toolIntentContext(m.messages), client,
	)
	if err != nil {
		cancel()
		return requestSession{}, err
	}
	if err := m.activateToolRegistry(registry, cancel); err != nil {
		return requestSession{}, err
	}

	tools := registry.Specs()
	if client.Capabilities().CustomApplyPatch {
		for i := range tools {
			if tools[i].Name == "fs_apply_patch" {
				tools[i].WireType = "custom"
				tools[i].WireName = "apply_patch"
			}
		}
	}
	if providerWebSearch {
		tools = append(tools, proto.ToolSpec{Name: "web_search", WireType: "web_search"})
	}
	debugTools(tools, registry.Len())

	request := proto.Request{
		Messages:   m.messages,
		API:        mod.API,
		Model:      mod.Name,
		User:       requestUser,
		Tools:      tools,
		TrackUsage: cfg.ShowTokenUsage,
		ToolCaller: m.toolCaller(registry, cfg),
	}
	if client.Capabilities().JSONResponseFormat && cfg.Format == "json" {
		request.ResponseFormat = &cfg.Format
	}

	m.debugRequest(cfg, &mod, &m.messages, tools, &request)
	m.thinkActive = thinkActive
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

func (m *Mods) sanitizeProviderContinuation(mod Model, useResponses bool) {
	allowed := allowedProviderDataKeys(mod, useResponses)
	crossProvider := false
	if m.db != nil && m.Config.SessionReadFromID != "" {
		if session, err := m.db.Find(m.Config.SessionReadFromID); err == nil && session != nil && session.API != nil {
			crossProvider = !strings.EqualFold(strings.TrimSpace(*session.API), strings.TrimSpace(mod.API))
			if crossProvider {
				debug.Printf(
					"Session provider changed: saved=%s/%s current=%s/%s; dropping opaque provider continuation data",
					*session.API, stringPtrValue(session.Model), mod.API, mod.Name,
				)
			}
		}
	}
	for i := range m.messages {
		if len(m.messages[i].ProviderData) == 0 {
			continue
		}
		if crossProvider || len(allowed) == 0 {
			m.messages[i].ProviderData = nil
			continue
		}
		filtered := make(map[string]json.RawMessage)
		for key, value := range m.messages[i].ProviderData {
			if _, ok := allowed[key]; ok {
				filtered[key] = value
			}
		}
		if len(filtered) == 0 {
			m.messages[i].ProviderData = nil
		} else {
			m.messages[i].ProviderData = filtered
		}
	}
}

func allowedProviderDataKeys(mod Model, useResponses bool) map[string]struct{} {
	switch modelProtocol(mod) {
	case "anthropic":
		return map[string]struct{}{"anthropic.messages.content": {}}
	case "openai", "github-copilot", "azure":
		profile := string(modelProviderProfile(mod))
		if useResponses {
			key := "openai.responses.output.v1"
			if profile == "deepseek" {
				key = "deepseek.responses.output.v1"
			}
			return map[string]struct{}{key: {}, "openai.responses.output": {}}
		}
		if profile == "deepseek" {
			return map[string]struct{}{"deepseek.chat.assistant.v1": {}}
		}
	}
	return nil
}

func stringPtrValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (m *Mods) activateToolRegistry(registry *toolregistry.Registry, cancel context.CancelFunc) error {
	if err := m.injectToolSelectionPrompt(registry); err != nil {
		_ = registry.Close()
		if cancel != nil {
			cancel()
		}
		m.currentToolRegistry = nil
		return err
	}
	m.currentToolRegistry = registry
	m.injectSelfHelpFallback()
	return nil
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

func applyHTTPProxy(cfg *Config, accfg *anthropic.Config, gccfg *google.Config, occfg *ollama.Config, ccfg *openai.Config) error {
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
	occfg.HTTPClient = httpClient
	return nil
}

func debugTools(tools []proto.ToolSpec, total int) {
	if !debug.Enabled() {
		return
	}
	lines := make([]string, 0, len(tools))
	for _, t := range tools {
		wireType := t.WireType
		if wireType == "" {
			wireType = "function"
		}
		wireName := t.WireName
		if wireName == "" {
			wireName = t.Name
		}
		lines = append(lines, fmt.Sprintf("%s · wire=%s/%s", t.Name, wireType, wireName))
	}
	debug.Print(DebugSection{
		Title:  "startup · tools",
		Fields: []DebugField{{Label: "available", Value: fmt.Sprintf("%d advertised · %d local executable", len(tools), total)}},
		Blocks: []DebugBlock{{Label: "registry", Value: strings.Join(lines, "\n")}},
	})
}

func (m *Mods) toolCaller(registry *toolregistry.Registry, cfg *Config) proto.ToolCaller {
	preflight := newCommandPreflightGate(cfg)
	return func(call proto.ToolCallRequest) (output string, returnErr error) {
		name := call.Name
		data := registry.UnwrapArguments(call.Name, call.Arguments)
		unwrappedArgs := !bytes.Equal(data, call.Arguments)
		started := time.Now()
		approvalRecord := approvalTrace{Source: "not reached"}
		redactedArgs := string(data)
		if m.secrets != nil {
			redactedArgs = m.secrets.Redact(redactedArgs)
		}
		title := fmt.Sprintf("tool · turn %d · round %d · call %d/%d", m.debugTurn, m.debugRound, call.Index, call.Total)
		callValue := fmt.Sprintf("→ %s [%s]", name, call.ID)
		if unwrappedArgs {
			callValue += " · unwrapped arguments"
		}
		debug.Print(DebugSection{
			Title: title + " · start",
			Fields: []DebugField{
				{Label: "call", Value: callValue},
			},
			Blocks: []DebugBlock{debug.Arguments([]byte(redactedArgs))},
		})
		defer func() {
			duration := time.Since(started)
			redactedOutput := output
			if m.secrets != nil {
				redactedOutput = m.secrets.Redact(redactedOutput)
			}
			status, symbol := toolDebugStatus(returnErr)
			fields := []DebugField{
				{Label: "call", Value: fmt.Sprintf("%s %s [%s]", symbol, name, call.ID)},
				{Label: "approval", Value: approvalDebugValue(approvalRecord)},
				{Label: "status", Value: fmt.Sprintf("%s · %s", status, formatDebugDuration(duration))},
			}
			if returnErr != nil {
				fields = append(fields, DebugField{Label: "error", Value: returnErr.Error()})
			}
			debug.Print(DebugSection{
				Title:  title + " · " + status,
				Fields: fields,
				Blocks: []DebugBlock{debug.Result(redactedOutput)},
			})
			m.debugToolTotal++
			switch {
			case status == "success":
				m.debugToolSucceeded++
			case strings.HasPrefix(status, "exit "):
				m.debugToolExited++
			case status == "denied":
				m.debugToolDenied++
			case status == "correction":
				m.debugToolCorrected++
			case status == "cancelled":
				m.debugToolCancelled++
			default:
				m.debugToolFailed++
			}
		}()

		ctx, cancel := m.toolCallContext(registry, name, cfg)
		m.addCancel(cancel)
		defer cancel()
		// Reject malformed calls (missing/empty required args) before computing
		// access intent or asking for approval. Without this, a call that omits
		// a required path renders a misleading "unknown target" review prompt
		// for an operation that would fail in Call anyway.
		if err := registry.ValidateRequiredArgs(name, data); err != nil {
			return "", err
		}
		if registry.Interactive(name) {
			approvalRecord = approvalTrace{Source: "interactive", Detail: "tool-managed"}
			m.sendToolOperationStatus(redactRemoteURLsForDisplay(ToolOperationLabel(name, data, m.width)))
			return registry.Call(ctx, name, data)
		}
		var processBinding toolregistry.ProcessProgramBinding
		if name == "process_run" {
			var prepareErr error
			processBinding, prepareErr = toolregistry.PrepareProcessProgram(data)
			if prepareErr != nil {
				return "", prepareErr
			}
			ctx = toolregistry.WithProcessProgramBinding(ctx, processBinding)
		}
		var assessment *approval.CommandAssessment
		if registry.ShellExecution(name) {
			command := ExtractShellCommand(data)
			if name == "process_run" {
				command = string(data)
			}
			assessed := m.assessCommandWithEnv(name, command, extractSecretEnvNames(data))
			if name == "process_run" {
				assessed = m.constrainResolvedProcessAssessment(assessed, processBinding)
			}
			assessment = &assessed
			if err := preflight.check(name, assessed); err != nil {
				return "", err
			}
		}
		intent := buildAccessIntent(name, data, registry, assessment)
		m.sendToolOperationStatus(redactRemoteURLsForDisplay(ToolOperationLabel(name, data, m.width)))
		scope := m.reviewer.scope
		safeDirs := m.safeDirs()
		intent = normalizeAccessIntentDirs(intent, scope.Value, name, registry.ShellExecution(name))

		// Inject authorized external directories so resolveWorkspacePath honors
		// approval. This applies whether or not review is skipped below: a
		// saved DirAllow rule may auto-approve the call, but the tool still
		// needs the authorization to touch the external path.
		if ext := ExternalDirs(intent, scope, safeDirs); len(ext) > 0 {
			ctx = toolregistry.WithAuthorizedDirs(ctx, ext)
		}

		// Every call goes through one policy entry point. requestApproval
		// performs saved-rule and access-matrix evaluation and returns
		// immediately for calls that do not need an interactive prompt.
		if err := m.reviewer.requestApproval(reviewerDeps{
			ctx:            m.ctx,
			shellExecution: registry.ShellExecution(name),
			assessment:     assessment,
			accessIntent:   intent,
			safeDirs:       safeDirs,
			onDecision: func(trace approvalTrace) {
				approvalRecord = trace
			},
		}, name, data); err != nil {
			return "", err
		}
		callData := data
		if m.secrets != nil {
			var err error
			callData, _, err = m.secrets.Resolve(name, data)
			if err != nil {
				return "", err
			}
		}
		output, err := registry.Call(ctx, name, callData)
		if m.secrets != nil {
			output = m.secrets.Redact(output)
		}
		if err != nil && m.secrets != nil {
			redacted := m.secrets.Redact(err.Error())
			if redacted != err.Error() {
				err = errors.New(redacted)
			}
		}
		return output, err
	}
}

func approvalDebugValue(trace approvalTrace) string {
	value := trace.Source
	if trace.Detail != "" {
		value += " · " + trace.Detail
	}
	if trace.Duration > 0 {
		value += " · " + formatDebugDuration(trace.Duration)
	}
	return value
}

func toolDebugStatus(err error) (status, symbol string) {
	if err == nil {
		return "success", "✓"
	}
	var correction correctionSuggester
	if errors.As(err, &correction) && correction.CorrectionSuggested() {
		return "correction", "!"
	}
	var exitErr shellExitCoder
	if errors.As(err, &exitErr) {
		return fmt.Sprintf("exit %d", exitErr.ExitCode()), "!"
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "cancelled", "!"
	}
	if errors.Is(err, errReviewUnavailable) || strings.Contains(err.Error(), "execution denied by user") {
		return "denied", "✗"
	}
	return "failed", "✗"
}

func formatDebugDuration(duration time.Duration) string {
	if duration < time.Millisecond {
		return "<1 ms"
	}
	return duration.Round(time.Millisecond).String()
}
