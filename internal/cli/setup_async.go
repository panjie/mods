package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	tea "charm.land/bubbletea/v2"
)

type setupResult struct {
	request        uint64
	provider, kind string
	models         []string
	endpoints      map[string]string
	device         copilotDeviceCode
	token          string
	err            error
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
	return func() tea.Msg {
		if protocol == "github-copilot" {
			result.models, result.endpoints, result.err = discoverCopilotModelsContext(ctx, endpoint, credential)
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
	if _, err := m.saveData(); err != nil {
		m.err = err
		return nil
	}
	d := m.draft()
	// Preserve the existing eligibility rules for the optional connection check.
	if d.key == "" || m.endpoint() == "" || !isOpenAICompatible(m.protocol()) {
		return m.commit()
	}
	ctx, result := m.begin("Testing connection")
	model, endpoint, credential := d.defaultModel, m.endpoint(), d.key
	return func() tea.Msg { result.err = testConnectionContext(ctx, model, endpoint, credential); return result }
}
func (m *setupModel) receive(result setupResult) tea.Cmd {
	if result.request != m.request || result.provider != m.api() || m.canceled {
		return nil
	}
	m.busy = ""
	switch result.kind {
	case "Discovering models":
		d := m.draft()
		d.discovered = true
		d.discoveryErr = result.err
		d.models = result.models
		d.endpoints = result.endpoints
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
	data.copilotModelEndpoints = d.endpoints
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
