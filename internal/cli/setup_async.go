package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/chatgpt"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/providerinfo"
)

type setupResult struct {
	request        uint64
	provider, kind string
	models         []string
	endpoints      map[string]string
	unconfirmed    []string
	device         copilotDeviceCode
	token          string
	err            error
	browserErr     error
	authURL        string
	authDone       <-chan error
}

func (m *setupModel) begin(kind string) (context.Context, setupResult) {
	m.stopRequest()
	ctx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.busy = kind
	m.err = nil
	return ctx, setupResult{request: m.request, provider: m.api(), kind: kind}
}
func (m *setupModel) discover() tea.Cmd {
	ctx, result := m.begin("Discovering models")
	d := m.draft()
	protocol, endpoint, credential := m.protocol(), m.endpoint(), resolveKeyForDiscovery(m.api(), d.key)
	sessionID := m.sessionID
	return func() tea.Msg {
		if result.provider == "chatgpt" {
			result.models, result.err = chatgpt.NewClient().Models(ctx)
		} else if protocol == "github-copilot" {
			result.models, result.endpoints, result.err = discoverCopilotModelsContext(ctx, endpoint, credential)
		} else if providerinfo.IsOpenCodeGoURL(endpoint) && (protocol == "openai" || protocol == "anthropic") {
			result.models, result.endpoints, result.unconfirmed, result.err = discoverOpenCodeGoModelsContext(ctx, protocol, endpoint, credential, sessionID)
		} else {
			result.models, result.err = discoverModelsContext(ctx, protocol, endpoint, credential)
		}
		if result.err == nil && len(result.models) == 0 {
			result.err = errors.New("No models returned")
		}
		return result
	}
}
func (m *setupModel) authenticate() tea.Cmd {
	ctx, result := m.begin("Starting GitHub authorization")
	return func() tea.Msg { result.device, result.err = startCopilotDeviceFlow(ctx); return result }
}
func (m *setupModel) checkConnection() tea.Cmd {
	data, err := m.saveData()
	if err != nil {
		m.err = err
		return nil
	}
	d := m.draft()
	credential := resolveKeyForDiscovery(m.api(), d.key)
	if m.api() != "chatgpt" && (credential == "" || m.endpoint() == "" || !isOpenAICompatible(m.protocol())) {
		return m.commit()
	}
	ctx, result := m.begin("Testing connection")
	model, _ := configWizardConfiguredModel(m.api(), d.defaultModel)
	profile := ""
	for _, api := range config.APIs {
		if api.Name == m.api() {
			profile = api.ProviderProfile
		}
	}
	endpoint := providerinfo.OpenAIEndpoint(m.api(), d.defaultModel, m.endpoint(), data.modelEndpoints[d.defaultModel])
	cfg := openai.Config{
		BaseURL: m.endpoint(), AuthToken: credential, UseResponses: endpoint == "responses",
		ProviderProfile: openai.ProviderProfile(providerinfo.Profile(m.api(), profile, model.ProviderProfile)),
		ExtraParams:     model.ExtraParams, Headers: providerinfo.OpenCodeHeaders(m.endpoint(), m.sessionID),
	}
	if m.api() == "chatgpt" {
		cfg.BaseURL = chatgpt.BaseURL
		cfg.HTTPClient = chatgpt.NewClient().HTTPClient()
		cfg.ProviderProfile = openai.ProviderProfileChatGPT
		cfg.UseResponses = true
	}
	modelName := d.defaultModel
	return func() tea.Msg { result.err = testConnectionContext(ctx, modelName, cfg); return result }
}
func (m *setupModel) receive(result setupResult) tea.Cmd {
	if result.request != m.request || result.provider != m.api() || m.canceled {
		return nil
	}
	m.busy = ""
	switch result.kind {
	case "Starting ChatGPT authorization":
		if result.err != nil {
			m.err = result.err
			return nil
		}
		m.device.VerificationURI = result.authURL
		m.browserErr = result.browserErr
		m.busy = "Waiting for ChatGPT authorization"
		next := setupResult{request: m.request, provider: m.api(), kind: m.busy}
		return func() tea.Msg { next.err = <-result.authDone; return next }
	case "Waiting for ChatGPT authorization":
		if result.err != nil {
			m.err = result.err
			return nil
		}
		m.invalidate()
		return m.move(false)
	case "Discovering models":
		d := m.draft()
		d.discovered = true
		d.discoveryErr = result.err
		d.models = result.models
		d.endpoints = result.endpoints
		d.unconfirmed = result.unconfirmed
		if result.err == nil {
			if len(d.selected) == 0 {
				d.selected = m.catalog.preselectedDiscoveredModels(m.api(), result.models)
			}
			// Keep only choices still offered by the current endpoint.
			d.selected = slices.DeleteFunc(d.selected, func(v string) bool { return !slices.Contains(result.models, v) })
		}
		if result.err != nil {
			d.selected = nil
		}
		m.err = result.err
		m.cursor = 0
	case "Starting GitHub authorization":
		if result.err != nil {
			m.err = result.err
			return nil
		}
		m.device = result.device
		ctx, next := m.begin("Waiting for GitHub authorization")
		return func() tea.Msg { next.token, next.err = pollCopilotDeviceFlow(ctx, result.device); return next }
	case "Waiting for GitHub authorization":
		if result.err != nil {
			m.err = result.err
			return nil
		}
		if result.token == "" {
			m.err = errors.New("GitHub returned an empty token")
			return nil
		}
		m.draft().key = result.token
		m.draft().storage = "config"
		m.invalidate()
		return m.move(false)
	case "Testing connection":
		if result.err != nil {
			cmd := m.enter(setupConnectionFailed)
			m.err = result.err
			return cmd
		}
		return m.commit()
	}
	return nil
}
func (m *setupModel) saveData() (configWizardSaveData, error) {
	d := m.draft()
	names, err := configWizardModelNames(m.api(), d.selected, d.manual)
	if err != nil {
		return configWizardSaveData{}, err
	}
	if err = validateConfigWizardDefaultModel(d.defaultModel, names); err != nil {
		return configWizardSaveData{}, err
	}
	data := m.data
	data.apiName = m.api()
	data.apiType = ""
	if m.provider == addProviderOption {
		data.apiType = d.protocol
	}
	data.modelName = d.defaultModel
	data.addedModelNames = names
	data.modelEndpoints = make(map[string]string, len(names))
	for _, name := range names {
		model, exists := configWizardConfiguredModel(m.api(), name)
		endpoint := d.endpoints[name]
		if providerinfo.IsOpenCodeGoURL(m.endpoint()) {
			route, known := providerinfo.OpenCodeGoRoute(m.endpoint(), name)
			if known && route.Protocol != m.protocol() {
				return data, fmt.Errorf("model %s uses %s; choose a provider with that API type", name, route.Protocol)
			}
			if !known && (!exists || (m.protocol() == "openai" && model.Endpoint == "")) {
				return data, fmt.Errorf("protocol for %s is unconfirmed; configure this model and its endpoint manually in mods.yml", name)
			}
			if known && route.Protocol == "openai" {
				endpoint = route.Endpoint
			}
		}
		if model.Endpoint != "" {
			endpoint = model.Endpoint
		}
		if endpoint != "" {
			data.modelEndpoints[name] = endpoint
		}
	}
	data.keyStorage = d.storage
	data.apiKey = d.key
	data.envVarName = resolveEnvVar(m.api())
	data.baseURLInput = m.endpoint()
	data.webSearchProviderValue = webSearchProviderForConfig(data.webSearchProvider, data.webSearchProviderValue)
	if m.savePath() == "" {
		return data, errors.New("Config file location is unavailable")
	}
	return data, nil
}
func (m *setupModel) commit() tea.Cmd {
	data, err := m.saveData()
	if err != nil {
		m.err = err
		return nil
	}
	// File writes are a short, non-cancelable commit on the UI goroutine. Keeping
	// the commit here prevents an Esc/late-message race from saving after cancel.
	m.stopRequest()
	if err = m.save(m.savePath(), m.previousPath, data); err != nil {
		var warning *setupCleanupWarning
		if errors.As(err, &warning) {
			m.warnings = append(m.warnings, warning.Error())
			m.done = true
			m.err = nil
			return tea.Quit
		}
		m.page = setupSummary
		m.err = err
		return nil
	}
	m.done = true
	m.err = nil
	return tea.Quit
}

func writeSetupConfig(savePath, previousPath string, data configWizardSaveData) error {
	if err := os.MkdirAll(filepath.Dir(savePath), 0o700); err != nil {
		return fmt.Errorf("prepare config directory: %w", err)
	}
	if err := WriteDefaultFile(savePath); err != nil {
		return fmt.Errorf("prepare config file: %w", err)
	}
	if err := SaveFieldPaths(savePath, buildConfigWizardUpdates(data)); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	if savePath != previousPath && (config.PortableDir != "" || !config.SettingsExisted) {
		if err := os.Remove(previousPath); err != nil && !os.IsNotExist(err) {
			return &setupCleanupWarning{fmt.Errorf("configuration saved, but could not remove previous file %s: %w", previousPath, err)}
		}
	}
	return nil
}

// A completed write with cleanup trouble is a success with a warning, not a
// retryable write failure. This preserves the previous portable migration rule.
type setupCleanupWarning struct{ error }
