package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/anthropic"
	cfgpkg "github.com/panjie/mods/internal/config"
)

const (
	addProviderOption         = "__mods_add_provider__"
	addModelOption            = "__mods_add_model__"
	defaultNewModelInputChars = 1000000
)

// RunConfigWizard launches an interactive TUI that guides the user through
// the essential mods setup: provider, model, API key, built-in tools, and
// review mode. Results are saved to the config file via yaml.Node round-trip,
// preserving existing comments.
func RunConfigWizard() error {
	// Pre-fill with current config values.
	chosenAPI := config.API
	var apiKey, keyStorage, baseURL, newProviderName string
	// apiType is the protocol chosen for a newly added provider (the page is
	// only shown then). "openai" means OpenAI-compatible and writes nothing.
	apiType := "openai"
	fsMode := string(config.BuiltinTools.Filesystem)
	if fsMode == "" {
		fsMode = "auto"
	}
	shellOn := config.BuiltinTools.Shell
	thinkingOn := config.BuiltinTools.SequentialThinking
	webSearchOn := config.WebSearch
	webSearchProvider := normalizeWebSearchProviderForWizard(config.WebSearchProvider)
	webSearchCustomURL := webSearchCustomURLForWizard(config.WebSearchProvider)
	webSearchKeyStorage := "env"
	webSearchAPIKey := ""
	webSearchAPIKeyEnv := config.WebSearchAPIKeyEnv
	if webSearchAPIKeyEnv == "" {
		webSearchAPIKeyEnv = cfgpkg.DefaultWebSearchAPIKeyEnv
	}
	if config.WebSearchAPIKey != "" && os.Getenv(webSearchAPIKeyEnv) == "" {
		webSearchKeyStorage = "config"
		webSearchAPIKey = config.WebSearchAPIKey
	}
	reviewMode := string(config.ReviewMode)
	if reviewMode == "" {
		reviewMode = "auto"
	}

	// Default storage: "env" (recommended), unless a key is already saved.
	keyStorage = "env"

	providerOpts := buildProviderOptions()

	keymap := configWizardKeyMap()

	// Model discovery happens inside the form: the "discover models" page
	// fetches the list via OptionsFunc, which huh runs asynchronously with a
	// spinner. discoveredPick/manualModelsText are bound to the model-entry
	// pages and read after the form. (The MultiSelect preserves option order,
	// so discoveredPick is already in fetched/sorted order.)
	var (
		discoveredPick   []string
		manualModelsText string
	)
	discoverOptions := func() []huh.Option[string] {
		api := wizardProviderName(chosenAPI, newProviderName)
		eff := api
		if chosenAPI == addProviderOption {
			eff = apiType
		} else if at := findAPIType(api); at != "" {
			eff = at
		}
		base := strings.TrimSpace(baseURL)
		if base == "" {
			base = findBaseURL(api)
		}
		if base == "" {
			base = builtinBaseURL(api)
		}
		discovered, derr := discoverModels(eff, base, resolveKeyForDiscovery(api, apiKey))
		if derr != nil || len(discovered) == 0 {
			return nil
		}
		// Show every fetched model; already-configured ones are picked out
		// only at save time, so their curated metadata is preserved.
		const maxPickerModels = 200
		opts := make([]huh.Option[string], 0, min(len(discovered), maxPickerModels))
		for i, m := range discovered {
			if i >= maxPickerModels {
				break
			}
			opts = append(opts, huh.NewOption(m, m))
		}
		return opts
	}

	wizardTheme := configWizardTheme(config.Theme)
	form := huh.NewForm(
		// Page 1: Provider
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Provider").
				Description("Choose the API backend mods should use by default.").
				Options(providerOpts...).
				Value(&chosenAPI),
		).
			Title("mods setup").
			Description("Connect a provider and pick the model you want to start with."),

		// Page 2: New provider name
		huh.NewGroup(
			huh.NewInput().
				Title("New provider name").
				Description("Provider key to write under apis.").
				Placeholder("groq").
				Value(&newProviderName).
				Validate(validateNewProviderName),
		).
			Title("new provider").
			Description("Use lowercase letters, digits, '-' or '_'.").
			WithHideFunc(func() bool { return chosenAPI != addProviderOption }),

		// Page 2b: API type (custom providers only)
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("API type").
				Description("Protocol this endpoint speaks. Choose Anthropic for Claude proxies or gateways that implement the Messages API.").
				Options(
					huh.NewOption("OpenAI-compatible (chat/completions)", "openai"),
					huh.NewOption("Anthropic (Messages API)", "anthropic"),
				).
				Value(&apiType),
		).
			Title("api type").
			Description("Most third-party gateways are OpenAI-compatible.").
			WithHideFunc(func() bool { return chosenAPI != addProviderOption }),

		// Page 3: Base URL (editable for all providers, required for new ones)
		huh.NewGroup(
			huh.NewInput().
				TitleFunc(func() string {
					return fmt.Sprintf("Base URL for %s", wizardProviderName(chosenAPI, newProviderName))
				}, []any{&chosenAPI, &newProviderName}).
				Description("Provider-level API endpoint shared by all models on this provider.").
				PlaceholderFunc(func() string {
					if url := findBaseURL(chosenAPI); url != "" {
						return url
					}
					return builtinBaseURL(chosenAPI)
				}, &chosenAPI).
				Value(&baseURL).
				Validate(func(value string) error {
					return validateWizardBaseURL(chosenAPI, value)
				}),
		).
			Title("provider endpoint").
			Description("Set or update the provider base URL before choosing models."),

		// Page 6: API key storage method (skip for ollama)
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("API key").
				Description("Environment variables keep secrets out of the YAML file.").
				OptionsFunc(func() []huh.Option[string] {
					envVar := resolveEnvVar(wizardProviderName(chosenAPI, newProviderName))
					return []huh.Option[string]{
						huh.NewOption(fmt.Sprintf("Use environment variable (%s)", envVar), "env"),
						huh.NewOption("Save in config file", "config"),
					}
				}, []any{&chosenAPI, &newProviderName}).
				Value(&keyStorage),
		).
			Title("credentials").
			Description("Tell mods where to read the API key from.").
			WithHideFunc(func() bool { return wizardProviderName(chosenAPI, newProviderName) == "ollama" }),

		// Page 7: API key input (skip for ollama or env-var storage)
		huh.NewGroup(
			huh.NewInput().
				Title("Enter your API key").
				Description("The key is stored in plaintext in your config file.").
				Placeholder("sk-...").
				Password(true).
				Value(&apiKey),
		).
			Title("saved key").
			Description("Only use this on a machine and config file you control.").
			WithHideFunc(func() bool {
				return wizardProviderName(chosenAPI, newProviderName) == "ollama" || keyStorage != "config"
			}),

		// Discover models: live fetch with a spinner. Every provider goes
		// through this page; an empty list (fetch failed or the provider
		// doesn't implement a model-list endpoint) falls through to manual.
		huh.NewGroup(
			huh.NewMultiSelect[string]().
				TitleFunc(func() string {
					return fmt.Sprintf("Models for %s", wizardProviderName(chosenAPI, newProviderName))
				}, []any{&chosenAPI, &newProviderName}).
				Description("Select models to add. The first selected becomes the default. If the list is empty, continue to type names manually.").
				OptionsFunc(discoverOptions, []any{&chosenAPI, &newProviderName, &apiType, &baseURL, &apiKey}).
				Value(&discoveredPick),
		).
			Title("discover models").
			Description("Fetch the model list from the provider's API now."),

		// Manual model entry: fallback shown only when discovery came back empty
		// (or the user picked nothing), so the user can always finish setup.
		huh.NewGroup(
			huh.NewText().
				TitleFunc(func() string {
					return fmt.Sprintf("Models for %s", wizardProviderName(chosenAPI, newProviderName))
				}, []any{&chosenAPI, &newProviderName}).
				Description("Enter one model identifier per line. The first model becomes the default.").
				Placeholder("llama-3.3-70b-versatile\nllama-3.1-8b-instant").
				Lines(6).
				ExternalEditor(false).
				Value(&manualModelsText).
				Validate(func(value string) error {
					_, err := parseNewModelNames(wizardProviderName(chosenAPI, newProviderName), value)
					return err
				}),
		).
			Title("new models").
			Description("Type model identifiers manually.").
			WithHideFunc(func() bool { return len(discoveredPick) > 0 }),

		// Page 8: Built-in tools
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Filesystem").
				Description("Controls whether mods can read and write local files.").
				Options(
					huh.NewOption("Auto — activate when prompt mentions files", "auto"),
					huh.NewOption("Always on", "true"),
					huh.NewOption("Off", "false"),
				).
				Value(&fsMode),
			huh.NewConfirm().
				Title("Enable shell execution?").
				Description("Mods can run shell commands; each risky command is reviewed before execution.").
				Value(&shellOn),
			huh.NewConfirm().
				Title("Enable sequential thinking?").
				Description("A scratchpad tool for complex multi-step reasoning.").
				Value(&thinkingOn),
		).
			Title("built-in tools").
			Description("Decide which local capabilities mods can use."),

		// Page 9: Web search on/off
		huh.NewGroup(
			huh.NewConfirm().
				Title("Enable web search?").
				Description("Adds a web_search tool for current information when the provider supports tools.").
				Value(&webSearchOn),
		).
			Title("web search").
			Description("Let mods search the web during prompts when needed."),

		// Page 10: Web search provider
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Web search provider").
				Description("DuckDuckGo needs no key. Tavily requires an API key.").
				Options(
					huh.NewOption("DuckDuckGo - no API key", "duckduckgo"),
					huh.NewOption("Tavily - API key required", "tavily"),
					huh.NewOption("Custom URL - JSON search endpoint", "custom"),
				).
				Value(&webSearchProvider),
		).
			Title("search provider").
			Description("Choose where web_search sends queries.").
			WithHideFunc(func() bool { return !webSearchOn }),

		// Page 11: Custom web search URL
		huh.NewGroup(
			huh.NewInput().
				Title("Custom search URL").
				Description("Base URL for a search API that responds to /search?q=...&limit=... .").
				Placeholder("https://search.example.com").
				Value(&webSearchCustomURL).
				Validate(func(value string) error {
					value = strings.TrimSpace(value)
					if value == "" {
						return fmt.Errorf("custom search URL is required")
					}
					if !isHTTPURL(value) {
						return fmt.Errorf("custom search URL must start with http:// or https://")
					}
					return nil
				}),
		).
			Title("custom search").
			Description("Use a self-hosted or third-party search endpoint.").
			WithHideFunc(func() bool { return !webSearchOn || webSearchProvider != "custom" }),

		// Page 12: Web search API key storage
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Web search API key").
				Description("Environment variables keep secrets out of the YAML file.").
				OptionsFunc(func() []huh.Option[string] {
					return []huh.Option[string]{
						huh.NewOption(fmt.Sprintf("Use environment variable (%s)", webSearchAPIKeyEnv), "env"),
						huh.NewOption("Save in config file", "config"),
					}
				}, &webSearchAPIKeyEnv).
				Value(&webSearchKeyStorage),
		).
			Title("search credentials").
			Description("Tell mods where to read the web search API key from.").
			WithHideFunc(func() bool { return !webSearchOn || !webSearchProviderUsesKey(webSearchProvider) }),

		// Page 13: Web search API key input
		huh.NewGroup(
			huh.NewInput().
				Title("Enter your web search API key").
				Description("The key is stored in plaintext in your config file.").
				Placeholder("tvly-...").
				Password(true).
				Value(&webSearchAPIKey),
		).
			Title("saved search key").
			Description("Only use this on a machine and config file you control.").
			WithHideFunc(func() bool {
				return !webSearchOn || !webSearchProviderUsesKey(webSearchProvider) || webSearchKeyStorage != "config"
			}),

		// Page 14: Review mode
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Tool review").
				Description("Choose how often mods asks before running tools.").
				Options(
					huh.NewOption("Auto — review risky actions (default)", "auto"),
					huh.NewOption("Always — review every tool call", "always"),
					huh.NewOption("Never — no review (automation only)", "never"),
				).
				Value(&reviewMode),
		).
			Title("review").
			Description("Tune the approval behavior for tool execution."),
	).
		WithTheme(wizardTheme).
		WithLayout(configWizardLayoutForTheme(wizardTheme)).
		WithKeyMap(keymap).
		WithEscapeAbortConfirmation("Press Esc again to exit.")

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "\nCanceled.")
			return nil
		}
		return fmt.Errorf("config wizard: %w", err)
	}

	// Resolve provider identity and base URL.
	apiName := wizardProviderName(chosenAPI, newProviderName)
	envVarName := resolveEnvVar(apiName)
	providerBaseURL := strings.TrimSpace(baseURL)
	if providerBaseURL == "" {
		providerBaseURL = findBaseURL(apiName)
	}

	// Effective adapter protocol: built-in providers carry it in their name;
	// a newly added provider declares it via the api-type selection, and an
	// existing custom provider may declare it via its configured api-type.
	effType := apiName
	newProvider := chosenAPI == addProviderOption
	if newProvider {
		effType = apiType
	} else if at := findAPIType(apiName); at != "" {
		effType = at
	}

	// Models always come from the discover picker, falling back to the manual
	// text box when discovery yielded nothing. The first entry becomes the
	// default model.
	var addedModelNames []string
	if len(discoveredPick) > 0 {
		// The MultiSelect value is in option (fetched) order, so the first
		// entry is a deterministic default.
		addedModelNames = discoveredPick
	} else {
		parsed, perr := parseNewModelNames(apiName, manualModelsText)
		if perr != nil {
			return perr
		}
		addedModelNames = parsed
	}
	modelName := addedModelNames[0]

	// Connection test. Only the OpenAI-compatible adapter exposes a simple
	// /chat/completions probe; a newly added Anthropic provider is skipped
	// with a note (probing it with an OpenAI request would always fail).
	if apiKey != "" && providerBaseURL != "" && isOpenAICompatible(effType) {
		fmt.Fprintf(os.Stderr, "\nTesting connection to %s... ", apiName)
		if err := testConnection(modelName, providerBaseURL, apiKey); err != nil {
			fmt.Fprintf(os.Stderr, "⚠ %s\n", err)
			var saveAnyway bool
			if err := huh.NewConfirm().
				Title("Connection test failed. Save configuration anyway?").
				Value(&saveAnyway).
				Run(); err != nil || !saveAnyway {
				fmt.Fprintln(os.Stderr, "Not saved.")
				return nil
			}
		} else {
			fmt.Fprintln(os.Stderr, "✓ OK")
		}
	} else if newProvider && apiKey != "" && providerBaseURL != "" && !isOpenAICompatible(effType) {
		fmt.Fprintf(os.Stderr, "\nSkipping connection test for %s endpoint (%s); verify with a real prompt.\n", apiName, effType)
	}

	// Form 3: config file location (Standard vs Portable). Skipped when the
	// executable directory cannot be determined (e.g. `go run`), since
	// portable mode needs a real on-disk binary location to write next to.
	savePath := config.SettingsPath
	if exeDir := cfgpkg.ExeDir(); exeDir != "" {
		saveLocation := "standard"
		if config.PortableDir != "" {
			saveLocation = "portable"
		}
		portablePath := filepath.Join(exeDir, "mods.yml")
		storageTheme := configWizardTheme(config.Theme)
		form3 := huh.NewForm(
			huh.NewGroup(
				huh.NewSelect[string]().
					Title("Config file location").
					Description("Portable stores the config and sessions next to this executable, so the whole folder is self-contained.").
					Options(
						huh.NewOption(fmt.Sprintf("Standard — %s", config.SettingsPath), "standard"),
						huh.NewOption(fmt.Sprintf("Portable — %s", portablePath), "portable"),
					).
					Value(&saveLocation),
			).
				Title("storage").
				Description("Choose where mods writes its configuration file."),
		).
			WithTheme(storageTheme).
			WithLayout(configWizardLayoutForTheme(storageTheme)).
			WithKeyMap(keymap).
			WithEscapeAbortConfirmation("Press Esc again to exit.")
		if err := form3.Run(); err != nil {
			if errors.Is(err, huh.ErrUserAborted) {
				fmt.Fprintln(os.Stderr, "\nCanceled.")
				return nil
			}
			return fmt.Errorf("config wizard: %w", err)
		}
		if saveLocation == "portable" {
			savePath = portablePath
		}
	}

	// Reflect the chosen path so the summary, save, and post-save message
	// all report the destination the user picked.
	previousPath := config.SettingsPath
	config.SettingsPath = savePath

	webSearchProviderValue := webSearchProviderForConfig(webSearchProvider, webSearchCustomURL)

	// Build the summary.
	printConfigSummary(summaryData{
		api:                 apiName,
		model:               modelName,
		apiType:             apiType,
		keyStorage:          keyStorage,
		envVarName:          envVarName,
		baseURL:             providerBaseURL,
		addedModelCount:     len(addedModelNames),
		fsMode:              fsMode,
		shellOn:             shellOn,
		thinkingOn:          thinkingOn,
		webSearchOn:         webSearchOn,
		webSearchProvider:   webSearchProviderValue,
		webSearchKeyStorage: webSearchKeyStorage,
		webSearchAPIKeyEnv:  webSearchAPIKeyEnv,
		reviewMode:          reviewMode,
		settingsPath:        config.SettingsPath,
	})

	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                apiName,
		apiType:                apiType,
		modelName:              modelName,
		reviewMode:             reviewMode,
		fsMode:                 fsMode,
		shellOn:                shellOn,
		thinkingOn:             thinkingOn,
		webSearchOn:            webSearchOn,
		webSearchProvider:      webSearchProvider,
		webSearchProviderValue: webSearchProviderValue,
		webSearchKeyStorage:    webSearchKeyStorage,
		webSearchAPIKey:        webSearchAPIKey,
		webSearchAPIKeyEnv:     webSearchAPIKeyEnv,
		keyStorage:             keyStorage,
		apiKey:                 apiKey,
		envVarName:             envVarName,
		baseURLInput:           baseURL,
		addedModelNames:        addedModelNames,
	})

	// Seed the target file when it doesn't yet exist (the common case when
	// bootstrapping portable mode for the first time). SaveFieldPaths is a
	// round-trip update and errors on a missing file, so ensure one exists.
	// WriteDefaultFile is a no-op when the file is already present.
	if err := WriteDefaultFile(savePath); err != nil {
		return fmt.Errorf("prepare config file: %w", err)
	}
	if err := SaveFieldPaths(savePath, updates); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// If Ensure auto-created the standard config during this run (it did
	// not exist before) and the user switched to portable, remove the
	// stray default file so the XDG location stays clean. This is best-effort
	// cleanup; surface non-NotExist failures so a permission issue is not
	// silently masked (the user would otherwise wonder why the old file
	// lingers and which location is authoritative).
	if savePath != previousPath && !config.SettingsExisted {
		if err := os.Remove(previousPath); err != nil && !os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "Warning: could not remove auto-created config %s: %v\n",
				StderrStyles().InlineCode.Render(previousPath), err)
		}
	}

	fmt.Fprintf(os.Stderr, "\nSaved to %s\n", StderrStyles().InlineCode.Render(savePath))

	if savePath != previousPath {
		fmt.Fprintln(os.Stderr, "Portable mode will be active on the next launch.")
	}

	if keyStorage == "env" && apiName != "ollama" {
		fmt.Fprintf(os.Stderr, "\nRemember to export your key:\n  export %s=sk-...\n",
			envVarName)
	}
	if webSearchOn && webSearchProviderUsesKey(webSearchProvider) && webSearchKeyStorage == "env" {
		fmt.Fprintf(os.Stderr, "\nRemember to export your web search key:\n  export %s=...\n", webSearchAPIKeyEnv)
	}

	return nil
}

func configWizardKeyMap() *huh.KeyMap {
	keymap := huh.NewDefaultKeyMap()
	back := func() key.Binding {
		return key.NewBinding(
			key.WithKeys("esc", "shift+tab"),
			key.WithHelp("esc", "back"),
		)
	}
	keymap.Input.Prev = back()
	keymap.FilePicker.Prev = back()
	keymap.Text.Prev = back()
	keymap.Select.Prev = back()
	keymap.MultiSelect.Prev = back()
	keymap.Note.Prev = back()
	keymap.Confirm.Prev = back()
	keymap.Text.NewLine = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)
	return keymap
}

// buildProviderOptions returns huh options for each configured provider,
// annotated with a short description for well-known providers.
func buildProviderOptions() []huh.Option[string] {
	descs := map[string]string{
		"openai":     "GPT-5.x, o3, o4-mini",
		"anthropic":  "Claude Opus/Sonnet/Haiku",
		"google":     "Gemini Pro/Flash",
		"deepseek":   "DeepSeek V4 (reasoning)",
		"glm":        "GLM-5.2 (Zhipu)",
		"qwen":       "Qwen (Alibaba)",
		"kimi":       "Kimi K2 (Moonshot)",
		"minimax":    "MiniMax M3",
		"openrouter": "Multi-provider aggregator",
		"ollama":     "Local models (no API key needed)",
		"cohere":     "Cohere Command",
		"azure":      "Azure OpenAI",
	}

	opts := make([]huh.Option[string], 0, len(config.APIs)+1)
	for _, api := range config.APIs {
		label := api.Name
		if desc, ok := descs[api.Name]; ok {
			label = fmt.Sprintf("%-12s  %s", api.Name, desc)
		}
		opts = append(opts, huh.NewOption(label, api.Name))
	}
	opts = append(opts, huh.NewOption("Add new provider", addProviderOption))
	return opts
}

const minConfigWizardFieldWidth = 20

type configWizardLayout struct {
	formHorizontalFrame  int
	fieldHorizontalFrame int
}

func configWizardLayoutForTheme(theme *huh.Theme) huh.Layout {
	formFrame := 0
	fieldFrame := 0
	if theme != nil {
		formFrame = theme.Form.Base.GetHorizontalFrameSize()
		fieldFrame = max(
			theme.Focused.Base.GetHorizontalFrameSize(),
			theme.Blurred.Base.GetHorizontalFrameSize(),
		)
	}
	return configWizardLayout{
		formHorizontalFrame:  formFrame,
		fieldHorizontalFrame: fieldFrame,
	}
}

func (l configWizardLayout) View(form *huh.Form) string {
	return huh.LayoutDefault.View(form)
}

func (l configWizardLayout) GroupWidth(_ *huh.Form, _ *huh.Group, width int) int {
	adjusted := width - l.formHorizontalFrame
	if adjusted < minConfigWizardFieldWidth {
		return minConfigWizardFieldWidth
	}
	return adjusted
}

func (l configWizardLayout) FieldWidth(_ *huh.Form, _ *huh.Group, groupWidth int) int {
	adjusted := groupWidth - l.fieldHorizontalFrame
	if adjusted < minConfigWizardFieldWidth {
		return minConfigWizardFieldWidth
	}
	return adjusted
}

func configWizardTheme(theme string) *huh.Theme {
	t := themeFrom(theme)
	accent := lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#8B7CFF"}
	accentSoft := lipgloss.AdaptiveColor{Light: "#E7E5FF", Dark: "#312B63"}
	text := lipgloss.AdaptiveColor{Light: "#202124", Dark: "#F2F2F7"}
	muted := lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	success := lipgloss.AdaptiveColor{Light: "#00875A", Dark: "#4ADE80"}
	border := lipgloss.AdaptiveColor{Light: "#D9D7FF", Dark: "#48406F"}

	t.Form.Base = t.Form.Base.Padding(1, 2)
	t.Group.Title = lipgloss.NewStyle().
		Foreground(accent).
		Bold(true).
		MarginBottom(1)
	t.Group.Description = lipgloss.NewStyle().
		Foreground(muted).
		MarginBottom(1)
	t.Focused.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder(), true).
		BorderForeground(border).
		Padding(1, 2)
	t.Focused.Card = t.Focused.Base
	t.Focused.Title = lipgloss.NewStyle().
		Foreground(text).
		Bold(true)
	t.Focused.Description = lipgloss.NewStyle().
		Foreground(muted)
	t.Focused.SelectSelector = lipgloss.NewStyle().
		Foreground(accent).
		Bold(true).
		SetString("▸ ")
	t.Focused.Option = lipgloss.NewStyle().Foreground(text)
	t.Focused.SelectedOption = lipgloss.NewStyle().
		Foreground(success).
		Bold(true)
	t.Focused.SelectedPrefix = lipgloss.NewStyle().
		Foreground(success).
		SetString("✓ ")
	t.Focused.UnselectedPrefix = lipgloss.NewStyle().
		Foreground(muted).
		SetString("  ")
	t.Focused.FocusedButton = lipgloss.NewStyle().
		Foreground(lipgloss.Color("#FFFFFF")).
		Background(accent).
		Bold(true).
		Padding(0, 2).
		MarginRight(1)
	t.Focused.BlurredButton = lipgloss.NewStyle().
		Foreground(text).
		Background(accentSoft).
		Padding(0, 2).
		MarginRight(1)
	t.Focused.TextInput.Prompt = lipgloss.NewStyle().Foreground(accent)
	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().Foreground(muted)
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().Foreground(accent)

	t.Blurred = t.Focused
	t.Blurred.Base = lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder(), true).
		Padding(1, 2)
	t.Blurred.Card = t.Blurred.Base
	t.Blurred.Title = lipgloss.NewStyle().Foreground(muted)
	t.Blurred.Description = lipgloss.NewStyle().Foreground(muted)
	t.Blurred.SelectSelector = lipgloss.NewStyle().SetString("  ")
	t.Blurred.NextIndicator = lipgloss.NewStyle()
	t.Blurred.PrevIndicator = lipgloss.NewStyle()

	t.FieldSeparator = lipgloss.NewStyle().SetString("\n")
	return t
}

func wizardProviderName(chosenAPI, newProviderName string) string {
	if chosenAPI == addProviderOption {
		return strings.TrimSpace(newProviderName)
	}
	return chosenAPI
}

func validateNewProviderName(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("provider name is required")
	}
	if value == addProviderOption || value == addModelOption {
		return fmt.Errorf("provider name is reserved")
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return fmt.Errorf("provider name may only contain lowercase letters, digits, '-' or '_'")
	}
	for _, api := range config.APIs {
		if api.Name == value {
			return fmt.Errorf("provider %q already exists", value)
		}
	}
	return nil
}

func validateNewModelName(provider, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("model name is required")
	}
	if value == addProviderOption || value == addModelOption {
		return fmt.Errorf("model name is reserved")
	}
	for _, api := range config.APIs {
		if api.Name != provider {
			continue
		}
		if _, ok := api.Models[value]; ok {
			return fmt.Errorf("model %q already exists for %s", value, provider)
		}
		break
	}
	return nil
}

func parseNewModelNames(provider, value string) ([]string, error) {
	seen := make(map[string]struct{})
	models := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		model := strings.TrimSpace(line)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		if err := validateNewModelName(provider, model); err != nil {
			return nil, err
		}
		seen[model] = struct{}{}
		models = append(models, model)
	}
	if len(models) == 0 {
		return nil, fmt.Errorf("model name is required")
	}
	return models, nil
}

func validateWizardBaseURL(chosenAPI, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		if chosenAPI == addProviderOption {
			return fmt.Errorf("base URL is required")
		}
		return nil
	}
	if !isHTTPURL(value) {
		return fmt.Errorf("base URL must start with http:// or https://")
	}
	return nil
}

// resolveEnvVar returns the configured api-key-env for the provider, or
// generates a sensible default (UPPERCASE_API_KEY) if not set.
func resolveEnvVar(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			if api.APIKeyEnv != "" {
				return api.APIKeyEnv
			}
			break
		}
	}
	return strings.ToUpper(strings.ReplaceAll(apiName, "-", "_")) + "_API_KEY"
}

// findBaseURL returns the configured base URL for the provider.
func findBaseURL(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			return api.BaseURL
		}
	}
	return ""
}

// builtinBaseURL returns the official default endpoint for a built-in
// provider. Used as a placeholder/hint in the --config wizard and as a
// fallback for model discovery when the user has not yet configured a
// base-url. Google's default URL embeds {model} — applyGoogleBaseURLOverride
// (internal/app/provider.go) substitutes it at runtime.
func builtinBaseURL(apiName string) string {
	switch apiName {
	case "google":
		return "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse"
	case "openai":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com/v1"
	case "ollama":
		return "http://localhost:11434"
	default:
		return "https://your-server.com/v1"
	}
}

// findAPIType returns the configured api-type (wire protocol) for the provider,
// or "" if unset (meaning name-based routing / OpenAI-compatible default).
func findAPIType(apiName string) string {
	for _, api := range config.APIs {
		if api.Name == apiName {
			return api.APIType
		}
	}
	return ""
}

// existingModelNames returns the set of model names already configured on the
// provider, so discovery can skip re-adding them.
func existingModelNames(apiName string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, api := range config.APIs {
		if api.Name != apiName {
			continue
		}
		for name := range api.Models {
			out[name] = struct{}{}
		}
		break
	}
	return out
}

func normalizeWebSearchProviderForWizard(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if isHTTPURL(provider) {
		return "custom"
	}
	switch provider {
	case "", "duckduckgo", "ddg", "google", "bing":
		return "duckduckgo"
	case "tavily":
		return "tavily"
	case "custom":
		return "custom"
	default:
		return "duckduckgo"
	}
}

func webSearchCustomURLForWizard(provider string) string {
	provider = strings.TrimSpace(provider)
	if isHTTPURL(provider) {
		return provider
	}
	return ""
}

func webSearchProviderForConfig(provider, customURL string) string {
	if provider == "custom" {
		return strings.TrimSpace(customURL)
	}
	return provider
}

func webSearchProviderUsesKey(provider string) bool {
	return provider == "tavily"
}

type configWizardSaveData struct {
	apiName, apiType, modelName, reviewMode, fsMode string
	webSearchProvider, webSearchProviderValue       string
	webSearchKeyStorage, webSearchAPIKey            string
	webSearchAPIKeyEnv                              string
	keyStorage, apiKey, envVarName, baseURLInput    string
	addedModelNames                                 []string
	shellOn, thinkingOn, webSearchOn                bool
}

func buildConfigWizardUpdates(d configWizardSaveData) []FieldUpdate {
	updates := []FieldUpdate{
		{Path: []string{"default-api"}, Value: d.apiName},
		{Path: []string{"default-model"}, Value: d.modelName},
		{Path: []string{"review-mode"}, Value: d.reviewMode},
		{Path: []string{"builtin-tools", "filesystem"}, Value: d.fsMode},
		{Path: []string{"builtin-tools", "shell"}, Value: d.shellOn},
		{Path: []string{"builtin-tools", "sequential-thinking"}, Value: d.thinkingOn},
		{Path: []string{"web-search"}, Value: d.webSearchOn},
		{Path: []string{"web-search-provider"}, Value: d.webSearchProviderValue},
	}
	if d.webSearchOn && webSearchProviderUsesKey(d.webSearchProvider) {
		if d.webSearchKeyStorage == "config" {
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key"}, Value: strings.TrimSpace(d.webSearchAPIKey)})
		} else {
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key"}, Value: nil})
			updates = append(updates, FieldUpdate{Path: []string{"web-search-api-key-env"}, Value: d.webSearchAPIKeyEnv})
		}
	}

	if d.apiName != "ollama" {
		if d.keyStorage == "config" && d.apiKey != "" {
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key"}, Value: d.apiKey})
		} else if d.envVarName != "" {
			updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-key-env"}, Value: d.envVarName})
		}
	}

	if d.baseURLInput != "" {
		updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "base-url"}, Value: strings.TrimSpace(d.baseURLInput)})
	}

	// A newly added provider may declare a non-OpenAI protocol (e.g. an
	// Anthropic Messages API gateway). "openai" is the default and writes
	// nothing, so existing OpenAI-compatible behavior is unchanged.
	if d.apiType != "" && d.apiType != "openai" {
		updates = append(updates, FieldUpdate{Path: []string{"apis", d.apiName, "api-type"}, Value: d.apiType})
	}

	// Add a max-input-chars default for each chosen model that isn't already
	// configured, so previously curated model entries keep their metadata.
	existing := existingModelNames(d.apiName)
	for _, modelName := range d.addedModelNames {
		modelName = strings.TrimSpace(modelName)
		if modelName == "" {
			continue
		}
		if _, ok := existing[modelName]; ok {
			continue
		}
		updates = append(updates, FieldUpdate{
			Path:  []string{"apis", d.apiName, "models", modelName, "max-input-chars"},
			Value: defaultNewModelInputChars,
		})
	}

	return updates
}

func isHTTPURL(value string) bool {
	value = strings.TrimSpace(value)
	return strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://")
}

// isOpenAICompatible reports whether the provider uses the standard
// OpenAI-compatible /chat/completions endpoint.
func isOpenAICompatible(apiName string) bool {
	switch apiName {
	case "anthropic", "google", "cohere", "ollama", "azure", "azure-ad":
		return false
	default:
		return true
	}
}

// testConnection makes a minimal chat completion request to verify the
// API key and endpoint work. Only meaningful for OpenAI-compatible providers.
func testConnection(model, baseURL, apiKey string) error {
	body := map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens": 5,
		"stream":     false,
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewReader(jsonBody))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	default:
		return nil
	}
}

// discoverModels queries a provider's list-models endpoint and returns the
// available model IDs. Supports OpenAI-compatible, Anthropic, and Ollama
// protocols. Best-effort: many Anthropic-compatible gateways do not implement
// /v1/models, so callers must handle errors and fall back to manual entry.
func discoverModels(apiType, baseURL, apiKey string) ([]string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	switch apiType {
	case "ollama":
		return fetchModelIDs(baseURL+"/api/tags", "", "", nil)
	case "anthropic":
		root := anthropic.NormalizeBaseURL(baseURL)
		headers := map[string]string{"anthropic-version": "2023-06-01"}
		ids, err := fetchModelIDs(root+"/v1/models?limit=1000", "x-api-key", apiKey, headers)
		if err == nil {
			return ids, nil
		}
		// Many Anthropic-compatible gateways omit /v1/models but expose an
		// OpenAI-style /models list; try it with the same auth headers.
		ids2, err2 := fetchModelIDs(root+"/models?limit=1000", "x-api-key", apiKey, headers)
		if err2 == nil {
			return ids2, nil
		}
		return nil, fmt.Errorf("%w (also tried /models: %v)", err, err2)
	case "google":
		return fetchGoogleModels(googleListModelsBase(baseURL) + "/models?key=" + url.QueryEscape(apiKey))
	default:
		// OpenAI-compatible: base URL typically ends in /v1; append /models.
		return fetchModelIDs(baseURL+"/models", "Authorization", "Bearer "+apiKey, nil)
	}
}

// fetchModelIDs performs a GET and extracts model identifiers from either an
// OpenAI/Anthropic-shaped response ({"data":[{"id":"..."}]}) or an
// Ollama-shaped one ({"models":[{"name":"..."}]}).
func fetchModelIDs(url, authHeader, authValue string, extraHeaders map[string]string) ([]string, error) {
	req, err := http.NewRequest("GET", url, nil) //nolint:gosec,noctx
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if authHeader != "" && authValue != "" {
		req.Header.Set(authHeader, authValue)
	}
	for k, v := range extraHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	}

	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ids := make([]string, 0)
	if raw, ok := body["data"]; ok {
		var arr []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(raw, &arr) == nil {
			for _, m := range arr {
				if m.ID != "" {
					ids = append(ids, m.ID)
				}
			}
		}
	}
	if len(ids) == 0 {
		if raw, ok := body["models"]; ok {
			var arr []struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(raw, &arr) == nil {
				for _, m := range arr {
					if m.Name != "" {
						ids = append(ids, m.Name)
					}
				}
			}
		}
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no models returned")
	}
	sort.Strings(ids)
	return ids, nil
}

// googleListModelsBase normalizes a Google base URL to the API root used for
// listing models. The --config wizard's fallback (builtinBaseURL) returns the
// full streamGenerateContent endpoint with a {model} placeholder, and users
// may paste a similar URL. This function strips any /models/... suffix and
// query/fragment so discoverModels can append /models?key=... cleanly.
// An empty base returns the public default root.
func googleListModelsBase(base string) string {
	base = strings.TrimSpace(base)
	if base == "" {
		return "https://generativelanguage.googleapis.com/v1beta"
	}
	u, err := url.Parse(base)
	if err != nil {
		return "https://generativelanguage.googleapis.com/v1beta"
	}
	path := u.Path
	if idx := strings.Index(path, "/models"); idx >= 0 {
		rest := path[idx+len("/models"):]
		if rest == "" || strings.HasPrefix(rest, "/") {
			path = path[:idx]
		}
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// fetchGoogleModels queries the Google Generative Language list-models endpoint
// and returns model IDs that support generateContent (filtering out embedding
// and text-only models). Auth is via the key= query parameter, not a header.
func fetchGoogleModels(urlStr string) ([]string, error) {
	req, err := http.NewRequest("GET", urlStr, nil) //nolint:gosec,noctx
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, fmt.Errorf("invalid API key (HTTP %d)", resp.StatusCode)
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("API error (HTTP %d)", resp.StatusCode)
	}

	var body struct {
		Models []struct {
			Name                       string   `json:"name"`
			SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	ids := make([]string, 0, len(body.Models))
	for _, m := range body.Models {
		name := strings.TrimPrefix(m.Name, "models/")
		if name == "" {
			continue
		}
		// Only keep models that support generateContent (chat/generation),
		// filtering out embedding, text-suffix, and other non-generative models.
		supportsGenerate := false
		for _, method := range m.SupportedGenerationMethods {
			if method == "generateContent" {
				supportsGenerate = true
				break
			}
		}
		if !supportsGenerate {
			continue
		}
		ids = append(ids, name)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("no generative models returned")
	}
	sort.Strings(ids)
	return ids, nil
}

// resolveKeyForDiscovery returns a usable API key for model discovery: the key
// just entered in the wizard if present, otherwise the provider's configured
// key (api-key or its env var). Returns "" if none is available.
func resolveKeyForDiscovery(apiName, enteredKey string) string {
	if k := strings.TrimSpace(enteredKey); k != "" {
		return k
	}
	for _, api := range config.APIs {
		if api.Name != apiName {
			continue
		}
		if api.APIKey != "" {
			return api.APIKey
		}
		if api.APIKeyEnv != "" {
			return os.Getenv(api.APIKeyEnv)
		}
		break
	}
	return ""
}

type summaryData struct {
	api, model, apiType, keyStorage, envVarName, baseURL string
	fsMode                                               string
	addedModelCount                                      int
	shellOn, thinkingOn, webSearchOn                     bool
	webSearchProvider, webSearchKeyStorage               string
	webSearchAPIKeyEnv                                   string
	reviewMode, settingsPath                             string
}

func printConfigSummary(d summaryData) {
	r := StderrRenderer()
	accent := lipgloss.AdaptiveColor{Light: "#5A56E0", Dark: "#8B7CFF"}
	muted := lipgloss.AdaptiveColor{Light: "#6B7280", Dark: "#9CA3AF"}
	border := lipgloss.AdaptiveColor{Light: "#D9D7FF", Dark: "#48406F"}

	title := r.NewStyle().
		Foreground(accent).
		Bold(true).
		Render("Configuration summary")
	labelStyle := r.NewStyle().
		Foreground(muted).
		Width(12)
	valueStyle := r.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#202124", Dark: "#F2F2F7"})

	modelValue := d.model
	if d.addedModelCount > 1 {
		modelValue += " (default, first line)"
	}
	rows := []string{
		summaryRow(labelStyle, valueStyle, "Provider", d.api),
		summaryRow(labelStyle, valueStyle, "Model", modelValue),
	}
	if d.addedModelCount > 0 {
		rows = append(rows, summaryRow(labelStyle, valueStyle, "Added models", fmt.Sprintf("%d", d.addedModelCount)))
	}

	if d.api != "ollama" {
		if d.keyStorage == "config" {
			rows = append(rows, summaryRow(labelStyle, valueStyle, "API key", "saved in config"))
		} else {
			rows = append(rows, summaryRow(labelStyle, valueStyle, "API key", "env var "+d.envVarName))
		}
	}
	if d.baseURL != "" {
		rows = append(rows, summaryRow(labelStyle, valueStyle, "Base URL", d.baseURL))
	}
	if d.apiType != "" && d.apiType != "openai" {
		rows = append(rows, summaryRow(labelStyle, valueStyle, "API type", d.apiType))
	}

	rows = append(rows,
		summaryRow(labelStyle, valueStyle, "Filesystem", d.fsMode),
		summaryRow(labelStyle, valueStyle, "Shell", boolLabel(d.shellOn)),
		summaryRow(labelStyle, valueStyle, "Thinking", boolLabel(d.thinkingOn)),
		summaryRow(labelStyle, valueStyle, "Web search", boolLabel(d.webSearchOn)),
	)
	if d.webSearchOn {
		rows = append(rows, summaryRow(labelStyle, valueStyle, "Search API", d.webSearchProvider))
		if webSearchProviderUsesKey(normalizeWebSearchProviderForWizard(d.webSearchProvider)) {
			if d.webSearchKeyStorage == "config" {
				rows = append(rows, summaryRow(labelStyle, valueStyle, "Search key", "saved in config"))
			} else {
				rows = append(rows, summaryRow(labelStyle, valueStyle, "Search key", "env var "+d.webSearchAPIKeyEnv))
			}
		}
	}

	rows = append(rows,
		summaryRow(labelStyle, valueStyle, "Review", d.reviewMode),
		summaryRow(labelStyle, valueStyle, "Config file", d.settingsPath),
	)

	body := strings.Join(rows, "\n")
	card := r.NewStyle().
		Border(lipgloss.RoundedBorder(), true).
		BorderForeground(border).
		Padding(1, 2).
		MarginTop(1).
		MarginBottom(1).
		Render(title + "\n" + r.NewStyle().Foreground(accent).Render(strings.Repeat("─", 24)) + "\n" + body)

	fmt.Fprintln(os.Stderr, card)
}

func summaryRow(labelStyle, valueStyle lipgloss.Style, label, value string) string {
	return labelStyle.Render(label) + valueStyle.Render(value)
}

func boolLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}
