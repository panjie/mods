package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/ui"
)

var errSetupCanceled = errors.New("interactive setup canceled")

type setupOption struct{ Key, Value string }

func newSetupOption(label, value string) setupOption { return setupOption{label, value} }

type setupPage int

const (
	setupProvider setupPage = iota
	setupName
	setupProtocol
	setupEndpoint
	setupCredentials
	setupKey
	setupAuth
	setupDiscovery
	setupManual
	setupDefault
	setupFilesystem
	setupShell
	setupSearch
	setupSearchProvider
	setupSearchURL
	setupSearchCredentials
	setupSearchKey
	setupReview
	setupStorage
	setupSummary
	setupConnectionFailed
)

type setupProviderDraft struct {
	endpoint, protocol, storage, key string
	models                           []string
	selected                         []string
	manual, defaultModel             string
	endpoints                        map[string]string
	discovered                       bool
	discoveryErr                     error
}

type setupModel struct {
	page                                     setupPage
	provider, newName                        string
	drafts                                   map[string]*setupProviderDraft
	catalog                                  configWizardProviderCatalog
	data                                     configWizardSaveData
	standardPath, portablePath, previousPath string
	input                                    textinput.Model
	area                                     textarea.Model
	filter                                   textinput.Model
	filtering                                bool
	manualScroll                             bool
	cursor, scroll, width, height            int
	isDark, backgroundKnown                  bool
	exitArmed, canceled, done                bool
	err                                      error
	busy                                     string
	request                                  uint64
	cancel                                   context.CancelFunc
	device                                   copilotDeviceCode
	picker                                   bool
	apis                                     []setupOption
	pickerModels                             map[string][]setupOption
	warnings                                 []string
	save                                     func(string, string, configWizardSaveData) error
}

func newSetupModel() *setupModel {
	m := &setupModel{provider: config.API, drafts: map[string]*setupProviderDraft{}, catalog: newConfigWizardProviderCatalog(config), width: 80, height: 24, isDark: ui.StderrIsDark(), backgroundKnown: ui.StaticBackgroundKnown(), previousPath: config.SettingsPath, save: writeSetupConfig}
	m.data = configWizardSaveData{fsMode: string(config.BuiltinTools.Filesystem), shellOn: config.BuiltinTools.Shell, webSearchOn: config.WebSearch, webSearchProvider: normalizeWebSearchProviderForWizard(config.WebSearchProvider), webSearchProviderValue: webSearchCustomURLForWizard(config.WebSearchProvider), webSearchKeyStorage: "env", webSearchAPIKeyEnv: config.WebSearchAPIKeyEnv, reviewMode: string(config.ReviewMode), portable: config.PortableDir != ""}
	if m.data.fsMode == "" {
		m.data.fsMode = "auto"
	}
	if m.data.reviewMode == "" {
		m.data.reviewMode = "auto"
	}
	if m.data.webSearchAPIKeyEnv == "" {
		m.data.webSearchAPIKeyEnv = cfgpkg.DefaultWebSearchAPIKeyEnv
	}
	if config.WebSearchAPIKey != "" && os.Getenv(m.data.webSearchAPIKeyEnv) == "" {
		m.data.webSearchKeyStorage = "config"
		m.data.webSearchAPIKey = config.WebSearchAPIKey
	}
	m.input = textinput.New()
	m.input.CharLimit = 0
	m.filter = textinput.New()
	m.filter.Prompt = "/ "
	m.filter.CharLimit = 0
	m.area = textarea.New()
	m.area.CharLimit = 0
	m.area.ShowLineNumbers = false
	m.area.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("ctrl+j"))
	m.apis = buildProviderOptions()
	if m.provider == "" && len(m.apis) > 0 {
		m.provider = m.apis[0].Value
	}
	m.prepare()
	return m
}

func RunConfigWizard() error {
	m := newSetupModel()
	var err error
	m.standardPath, err = cfgpkg.StandardSettingsPath()
	if err != nil {
		return fmt.Errorf("resolve standard config path: %w", err)
	}
	if dir := cfgpkg.ExeDir(); dir != "" {
		m.portablePath = filepath.Join(dir, "mods.yml")
	}
	if m.portablePath == "" {
		m.data.portable = false
	}
	if err = m.run(); errors.Is(err, errSetupCanceled) {
		fmt.Fprintln(os.Stderr, "Canceled.")
		return nil
	}
	if err != nil {
		return fmt.Errorf("config wizard: %w", err)
	}
	config.SettingsPath = m.savePath()
	for _, warning := range m.warnings {
		fmt.Fprintln(os.Stderr, "Warning: "+warning)
	}
	fmt.Fprintf(os.Stderr, "Saved to %s\n", m.savePath())
	if m.savePath() != m.previousPath {
		if m.data.portable {
			fmt.Fprintln(os.Stderr, "Portable mode will be active on the next launch.")
		} else {
			fmt.Fprintln(os.Stderr, "Standard mode will be active on the next launch.")
		}
	}
	d := m.draft()
	if d.storage == "env" && m.api() != "ollama" && m.api() != "github-copilot" {
		fmt.Fprintf(os.Stderr, "Remember to export your key:\n  export %s=sk-...\n", resolveEnvVar(m.api()))
	}
	if m.data.webSearchOn && webSearchProviderUsesKey(m.data.webSearchProvider) && m.data.webSearchKeyStorage == "env" {
		fmt.Fprintf(os.Stderr, "Remember to export your search key:\n  export %s=...\n", m.data.webSearchAPIKeyEnv)
	}
	return nil
}

func (m *setupModel) run() error {
	opts := buildTeaProgramOptions()
	if !config.InteractiveTTYAvailable {
		return errors.New("interactive setup requires a terminal")
	}
	defer m.stopRequest()
	_, err := tea.NewProgram(m, opts...).Run()
	if err != nil {
		return err
	}
	if m.canceled {
		return errSetupCanceled
	}
	return m.err
}

func runSetupPicker(apis []setupOption, models map[string][]setupOption) error {
	m := newSetupModel()
	m.picker = true
	m.apis = apis
	m.pickerModels = models
	if !slices.ContainsFunc(apis, func(o setupOption) bool { return o.Value == m.provider }) {
		m.provider = apis[0].Value
	}
	for name := range models {
		sort.Slice(models[name], func(i, j int) bool { return models[name][i].Key < models[name][j].Key })
	}
	m.prepare()
	if err := m.run(); err != nil {
		return err
	}
	config.API = m.provider
	config.Model = m.draft().defaultModel
	return nil
}

func (m *setupModel) api() string { return wizardProviderName(m.provider, m.newName) }
func (m *setupModel) draft() *setupProviderDraft {
	api := m.api()
	if d := m.drafts[api]; d != nil {
		return d
	}
	d := &setupProviderDraft{endpoint: findBaseURL(api), protocol: findAPIType(api), storage: "env", key: configuredAPIKey(api), manual: m.catalog.manualModelText(api)}
	if d.endpoint == "" {
		d.endpoint = builtinBaseURL(api)
	}
	if d.protocol == "" {
		d.protocol = "openai"
	}
	if d.key != "" {
		d.storage = "config"
	}
	if api == config.API {
		d.defaultModel = config.Model
	}
	m.drafts[api] = d
	return d
}
func (m *setupModel) protocol() string {
	if m.provider == addProviderOption {
		return configWizardDiscoveryType(m.provider, m.newName, m.draft().protocol)
	}
	if p := findAPIType(m.api()); p != "" {
		return p
	}
	return m.api()
}
func (m *setupModel) endpoint() string {
	if value := strings.TrimSpace(m.draft().endpoint); value != "" {
		return value
	}
	if value := findBaseURL(m.api()); value != "" {
		return value
	}
	return builtinBaseURL(m.api())
}
func (m *setupModel) savePath() string {
	if m.data.portable {
		return m.portablePath
	}
	return m.standardPath
}
func (m *setupModel) pages() []setupPage {
	if m.picker {
		return []setupPage{setupProvider, setupDefault}
	}
	pages := []setupPage{setupProvider}
	if m.provider == addProviderOption {
		pages = append(pages, setupName, setupProtocol)
	}
	pages = append(pages, setupEndpoint)
	switch m.api() {
	case "ollama":
	case "github-copilot":
		pages = append(pages, setupAuth)
	default:
		pages = append(pages, setupCredentials)
		if m.draft().storage == "config" {
			pages = append(pages, setupKey)
		}
	}
	pages = append(pages, setupDiscovery)
	if len(m.draft().selected) == 0 {
		pages = append(pages, setupManual)
	}
	pages = append(pages, setupDefault, setupFilesystem, setupShell, setupSearch)
	if m.data.webSearchOn {
		pages = append(pages, setupSearchProvider)
		if m.data.webSearchProvider == "custom" {
			pages = append(pages, setupSearchURL)
		}
		if webSearchProviderUsesKey(m.data.webSearchProvider) {
			pages = append(pages, setupSearchCredentials)
			if m.data.webSearchKeyStorage == "config" {
				pages = append(pages, setupSearchKey)
			}
		}
	}
	pages = append(pages, setupReview)
	if m.portablePath != "" {
		pages = append(pages, setupStorage)
	}
	return append(pages, setupSummary)
}
func (m *setupModel) Init() tea.Cmd { return tea.RequestBackgroundColor }
func (m *setupModel) stopRequest() {
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
	m.request++
	m.busy = ""
}
func (m *setupModel) enter(page setupPage) tea.Cmd {
	m.stopRequest()
	m.page = page
	m.err = nil
	m.cursor = 0
	m.scroll = 0
	m.manualScroll = false
	m.filtering = false
	m.filter.SetValue("")
	m.device = copilotDeviceCode{}
	cmd := m.prepare()
	if page == setupDiscovery && !m.draft().discovered {
		return m.discover()
	}
	if page == setupAuth && !configWizardWaitingForCopilotAuth(m.api(), m.draft().key) {
		return cmd
	}
	return cmd
}
func (m *setupModel) move(back bool) tea.Cmd {
	if back && m.page == setupConnectionFailed {
		return m.enter(setupSummary)
	}
	pages := m.pages()
	index := slices.Index(pages, m.page)
	if index < 0 {
		return m.enter(setupProvider)
	}
	if back {
		if index > 0 {
			return m.enter(pages[index-1])
		}
		if m.exitArmed {
			m.canceled = true
			return tea.Quit
		}
		m.exitArmed = true
		return nil
	}
	if index+1 < len(pages) {
		return m.enter(pages[index+1])
	}
	return nil
}
func (m *setupModel) invalidate() {
	m.stopRequest()
	d := m.draft()
	d.discovered = false
	d.models = nil
	d.endpoints = nil
	d.discoveryErr = nil
}
func (m *setupModel) prepare() tea.Cmd {
	m.input.Blur()
	m.area.Blur()
	m.filter.Blur()
	m.input.EchoMode = textinput.EchoNormal
	m.input.Prompt = "> "
	if m.page == setupKey || m.page == setupSearchKey {
		m.input.EchoMode = textinput.EchoPassword
	}
	if m.page == setupManual {
		m.area.SetValue(m.draft().manual)
		m.resize()
		return m.area.Focus()
	}
	if m.isInput() {
		m.input.SetValue(m.value())
		m.input.CursorEnd()
		m.resize()
		return m.input.Focus()
	}
	options := m.options()
	if m.page == setupDefault {
		names := make([]string, 0, len(options))
		for _, o := range options {
			names = append(names, o.Value)
		}
		m.draft().defaultModel = configWizardPreferredDefaultModel(m.draft().defaultModel, names)
	}
	m.cursor = 0
	for i, o := range options {
		if o.Value == m.value() {
			m.cursor = i
			break
		}
	}
	m.resize()
	return nil
}
func (m *setupModel) isInput() bool {
	switch m.page {
	case setupName, setupEndpoint, setupKey, setupSearchURL, setupSearchKey:
		return true
	}
	return false
}
func (m *setupModel) resize() {
	w := m.contentWidth()
	m.input.SetWidth(max(1, w-2))
	m.filter.SetWidth(max(1, w-2))
	m.area.SetWidth(w)
	m.area.SetHeight(max(1, min(6, m.height-9)))
	m.styleInputs()
}
func (m *setupModel) value() string {
	d := m.draft()
	switch m.page {
	case setupProvider:
		return m.provider
	case setupName:
		return m.newName
	case setupProtocol:
		return d.protocol
	case setupEndpoint:
		return d.endpoint
	case setupCredentials:
		return d.storage
	case setupKey:
		return d.key
	case setupDefault:
		return d.defaultModel
	case setupFilesystem:
		return m.data.fsMode
	case setupShell:
		if m.data.shellOn {
			return "on"
		}
		return "off"
	case setupSearch:
		if m.data.webSearchOn {
			return "on"
		}
		return "off"
	case setupSearchProvider:
		return m.data.webSearchProvider
	case setupSearchURL:
		return m.data.webSearchProviderValue
	case setupSearchCredentials:
		return m.data.webSearchKeyStorage
	case setupSearchKey:
		return m.data.webSearchAPIKey
	case setupReview:
		return m.data.reviewMode
	case setupStorage:
		if m.data.portable {
			return "portable"
		}
		return "standard"
	case setupConnectionFailed:
		return "no"
	}
	return ""
}
func (m *setupModel) setValue(value string) {
	d := m.draft()
	old := m.value()
	switch m.page {
	case setupProvider:
		m.provider = value
	case setupName:
		m.newName = value
	case setupProtocol:
		d.protocol = value
	case setupEndpoint:
		d.endpoint = value
	case setupCredentials:
		d.storage = value
	case setupKey:
		d.key = value
	case setupDefault:
		d.defaultModel = value
	case setupFilesystem:
		m.data.fsMode = value
	case setupShell:
		m.data.shellOn = value == "on"
	case setupSearch:
		m.data.webSearchOn = value == "on"
	case setupSearchProvider:
		m.data.webSearchProvider = value
	case setupSearchURL:
		m.data.webSearchProviderValue = value
	case setupSearchCredentials:
		m.data.webSearchKeyStorage = value
	case setupSearchKey:
		m.data.webSearchAPIKey = value
	case setupReview:
		m.data.reviewMode = value
	case setupStorage:
		m.data.portable = value == "portable"
	}
	if old != value {
		m.err = nil
		switch m.page {
		case setupProtocol, setupEndpoint, setupCredentials, setupKey:
			m.invalidate()
		}
	}
}
func (m *setupModel) options() []setupOption {
	switch m.page {
	case setupProvider:
		return m.apis
	case setupProtocol:
		return []setupOption{{"OpenAI-compatible (chat/completions)", "openai"}, {"Anthropic (Messages API)", "anthropic"}}
	case setupCredentials:
		return []setupOption{{"Use environment variable (" + resolveEnvVar(m.api()) + ")", "env"}, {"Save in config file", "config"}}
	case setupDiscovery:
		opts := make([]setupOption, 0, len(m.draft().models))
		for _, v := range m.draft().models {
			opts = append(opts, newSetupOption(configWizardModelOptionLabel(m.api(), v), v))
		}
		return opts
	case setupDefault:
		if m.picker {
			return m.pickerModels[m.api()]
		}
		names, _ := configWizardModelNames(m.api(), m.draft().selected, m.draft().manual)
		opts := make([]setupOption, 0, len(names))
		for _, v := range names {
			opts = append(opts, newSetupOption(configWizardModelOptionLabel(m.api(), v), v))
		}
		return opts
	case setupFilesystem:
		return []setupOption{{"Auto — activate when prompt mentions files", "auto"}, {"Always on", "true"}, {"Off", "false"}}
	case setupShell, setupSearch:
		return []setupOption{{"Yes", "on"}, {"No", "off"}}
	case setupSearchProvider:
		return []setupOption{{"Tavily — API key required", "tavily"}, {"Custom URL — JSON search endpoint", "custom"}}
	case setupSearchCredentials:
		return []setupOption{{"Use environment variable (" + m.data.webSearchAPIKeyEnv + ")", "env"}, {"Save in config file", "config"}}
	case setupReview:
		return []setupOption{{"Auto — review risky actions", "auto"}, {"Always — review every tool call", "always"}, {"Never — no review (automation only)", "never"}}
	case setupStorage:
		return []setupOption{{"Standard — " + m.standardPath, "standard"}, {"Portable — " + m.portablePath, "portable"}}
	case setupConnectionFailed:
		return []setupOption{{"Do not save — return to summary", "no"}, {"Save configuration anyway", "yes"}}
	}
	return nil
}
func (m *setupModel) filtered() []setupOption {
	opts := m.options()
	q := strings.ToLower(m.filter.Value())
	if !m.filtering || q == "" {
		return opts
	}
	out := []setupOption{}
	for _, o := range opts {
		if strings.Contains(strings.ToLower(o.Key), q) {
			out = append(out, o)
		}
	}
	return out
}
func (m *setupModel) validate() error {
	switch m.page {
	case setupName:
		return validateNewProviderName(m.newName)
	case setupEndpoint:
		return validateWizardBaseURL(m.provider, m.draft().endpoint)
	case setupManual:
		_, err := parseModelNames(m.api(), m.draft().manual, true)
		return err
	case setupDefault:
		if m.draft().defaultModel == "" {
			return errors.New("Choose a model")
		}
		if !m.picker {
			names, err := configWizardModelNames(m.api(), m.draft().selected, m.draft().manual)
			if err != nil {
				return err
			}
			return validateConfigWizardDefaultModel(m.draft().defaultModel, names)
		}
	case setupSearchURL:
		if !isHTTPURL(strings.TrimSpace(m.data.webSearchProviderValue)) {
			return errors.New("Custom search URL must start with http:// or https://")
		}
	}
	return nil
}
func (m *setupModel) accept() tea.Cmd {
	if m.page == setupAuth {
		if configWizardWaitingForCopilotAuth(m.api(), m.draft().key) {
			return m.authenticate()
		}
		return m.move(false)
	}
	if m.page == setupSummary {
		return m.checkConnection()
	}
	options := m.filtered()
	if m.filtering && len(options) == 0 {
		m.err = errors.New("No matching options")
		return nil
	}
	if len(options) > 0 && m.page != setupDiscovery {
		m.cursor = min(m.cursor, len(options)-1)
		if m.page == setupConnectionFailed {
			if options[m.cursor].Value == "yes" {
				return m.commit()
			}
			return m.enter(setupSummary)
		}
		m.setValue(options[m.cursor].Value)
	}
	if err := m.validate(); err != nil {
		m.err = err
		return nil
	}
	if m.picker && m.page == setupDefault {
		m.done = true
		return tea.Quit
	}
	return m.move(false)
}
func (m *setupModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.done || m.canceled {
		return m, nil
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize()
		return m, nil
	case tea.BackgroundColorMsg:
		m.isDark = msg.IsDark()
		m.backgroundKnown = true
		m.styleInputs()
		return m, nil
	case setupResult:
		return m, m.receive(msg)
	case tea.KeyPressMsg:
		k := msg.String()
		// Letter shortcuts belong to navigation, not editable text or filters.
		navigation := !m.isInput() && m.page != setupManual && !m.filtering
		if navigation {
			switch k {
			case "j":
				k = "down"
			case "k":
				k = "up"
			}
		}
		if k == "ctrl+c" || navigation && k == "q" {
			m.stopRequest()
			m.canceled = true
			return m, tea.Quit
		}
		if k != "esc" {
			m.exitArmed = false
		}
		if k == "esc" && m.filtering {
			m.filtering = false
			m.filter.SetValue("")
			m.cursor = 0
			return m, nil
		}
		if k == "shift+tab" && m.page == setupProvider {
			return m, nil
		}
		if k == "esc" || k == "shift+tab" {
			return m, m.move(true)
		}
		if m.width < 16 || m.height < 8 {
			return m, nil
		}
		if m.filtering {
			if k == "enter" {
				return m, m.accept()
			}
			if k != "up" && k != "down" && (k != "space" || m.page != setupDiscovery) {
				var cmd tea.Cmd
				m.filter, cmd = m.filter.Update(msg)
				m.cursor = 0
				return m, cmd
			}
		}
		if (k == "pgup" || k == "pgdown") && !m.isInput() && m.page != setupManual {
			m.manualScroll = true
			if k == "pgup" {
				m.scroll = max(0, m.scroll-max(1, m.height-8))
			} else {
				m.scroll += max(1, m.height-8)
			}
			return m, nil
		}

		if m.busy != "" {
			if k == "m" && m.page == setupDiscovery {
				m.draft().selected = nil
				return m, m.enter(setupManual)
			}
			return m, nil
		}
		if k == "enter" || k == "tab" {
			return m, m.accept()
		}
		if m.page == setupDiscovery && k == "r" && !m.filtering {
			return m, m.discover()
		}
		if m.page == setupDiscovery && k == "m" && !m.filtering {
			m.draft().selected = nil
			return m, m.enter(setupManual)
		}
		if k == "/" && !m.isInput() && m.page != setupManual && len(m.options()) > 0 && !m.filtering {
			m.filtering = true
			return m, m.filter.Focus()
		}
		if !m.isInput() && m.page != setupManual {
			opts := m.filtered()
			switch k {
			case "up":
				m.manualScroll = false
				m.cursor = max(0, m.cursor-1)
			case "down":
				m.manualScroll = false
				m.cursor = min(max(0, len(opts)-1), m.cursor+1)
			case "space":
				if m.page == setupDiscovery && len(opts) > 0 {
					v := opts[m.cursor].Value
					d := m.draft()
					if i := slices.Index(d.selected, v); i >= 0 {
						d.selected = slices.Delete(d.selected, i, i+1)
					} else {
						d.selected = append(d.selected, v)
					}
				}
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if m.filtering {
		m.filter, cmd = m.filter.Update(msg)
		return m, cmd
	}
	if m.isInput() {
		m.input, cmd = m.input.Update(msg)
		m.setValue(m.input.Value())
	}
	if m.page == setupManual {
		m.area, cmd = m.area.Update(msg)
		m.draft().manual = m.area.Value()
	}
	return m, cmd
}
