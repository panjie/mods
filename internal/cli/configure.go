package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
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
	chosenModel := config.Model
	var apiKey, keyStorage, baseURL, newProviderName, newModelNames string
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
		webSearchAPIKeyEnv = "TAVILY_API_KEY"
	}
	if config.WebSearchAPIKey != "" && os.Getenv(webSearchAPIKeyEnv) == "" {
		webSearchKeyStorage = "config"
		webSearchAPIKey = config.WebSearchAPIKey
	}
	reviewMode := string(config.ReviewMode)
	if reviewMode == "" {
		reviewMode = "mutable"
	}

	// Default storage: "env" (recommended), unless a key is already saved.
	keyStorage = "env"

	providerOpts := buildProviderOptions()

	keymap := huh.NewDefaultKeyMap()
	keymap.Text.NewLine = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)

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
				Description("OpenAI-compatible provider key to write under apis.").
				Placeholder("groq").
				Value(&newProviderName).
				Validate(validateNewProviderName),
		).
			Title("new provider").
			Description("Use lowercase letters, digits, '-' or '_'.").
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
					return "https://your-server.com/v1"
				}, &chosenAPI).
				Value(&baseURL).
				Validate(func(value string) error {
					return validateWizardBaseURL(chosenAPI, value)
				}),
		).
			Title("provider endpoint").
			Description("Set or update the provider base URL before choosing models."),

		// Page 4: Model for existing provider
		huh.NewGroup(
			huh.NewSelect[string]().
				TitleFunc(func() string {
					return fmt.Sprintf("Model for %s", chosenAPI)
				}, &chosenAPI).
				Description("Choose a model on this provider, or add another model that reuses this provider endpoint.").
				OptionsFunc(func() []huh.Option[string] {
					return buildModelOptions(chosenAPI)
				}, &chosenAPI).
				Value(&chosenModel),
		).
			Title("model").
			Description("Choose an existing model or add one to this provider.").
			WithHideFunc(func() bool { return chosenAPI == addProviderOption }),

		// Page 5: New model names
		huh.NewGroup(
			huh.NewText().
				TitleFunc(func() string {
					if chosenAPI == addProviderOption {
						return fmt.Sprintf("Models for %s", wizardProviderName(chosenAPI, newProviderName))
					}
					return fmt.Sprintf("New models for %s", chosenAPI)
				}, []any{&chosenAPI, &newProviderName}).
				Description("Enter one model identifier per line. The first model becomes the default.").
				Placeholder("llama-3.3-70b-versatile\nllama-3.1-8b-instant").
				Lines(4).
				ExternalEditor(false).
				Value(&newModelNames).
				Validate(func(value string) error {
					_, err := parseNewModelNames(wizardProviderName(chosenAPI, newProviderName), value)
					return err
				}),
		).
			Title("new models").
			Description("Add multiple models under the provider endpoint. Need a different base URL? Add a new provider instead.").
			WithHideFunc(func() bool {
				return chosenAPI != addProviderOption && chosenModel != addModelOption
			}),

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
					huh.NewOption("Mutable — review risky actions (default)", "mutable"),
					huh.NewOption("Always — review every tool call", "always"),
					huh.NewOption("Never — no review (automation only)", "never"),
				).
				Value(&reviewMode),
		).
			Title("review").
			Description("Tune the approval behavior for tool execution."),
	).
		WithTheme(configWizardTheme(config.Theme)).
		WithKeyMap(keymap)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "\nCanceled.")
			return nil
		}
		return fmt.Errorf("config wizard: %w", err)
	}

	// Resolve the final provider/model after handling the "add" choices.
	apiName, modelName, addedModelNames, providerBaseURL, addedModel, err := resolveWizardProviderModel(
		chosenAPI,
		chosenModel,
		newProviderName,
		newModelNames,
		baseURL,
	)
	if err != nil {
		return err
	}
	envVarName := resolveEnvVar(apiName)
	if providerBaseURL == "" {
		providerBaseURL = findBaseURL(apiName)
	}
	webSearchProviderValue := webSearchProviderForConfig(webSearchProvider, webSearchCustomURL)

	// Connection test (OpenAI-compatible providers only, with a key to test).
	if apiKey != "" && isOpenAICompatible(apiName) && providerBaseURL != "" {
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
	}

	// Build the summary.
	printConfigSummary(summaryData{
		api:                    apiName,
		model:                  modelName,
		keyStorage:             keyStorage,
		envVarName:             envVarName,
		baseURL:                providerBaseURL,
		addedModelCount:        len(addedModelNames),
		fsMode:                 fsMode,
		shellOn:                shellOn,
		thinkingOn:             thinkingOn,
		webSearchOn:            webSearchOn,
		webSearchProvider:      webSearchProviderValue,
		webSearchKeyStorage:    webSearchKeyStorage,
		webSearchAPIKeyEnv:     webSearchAPIKeyEnv,
		reviewMode:             reviewMode,
		settingsPath:           config.SettingsPath,
	})

	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                apiName,
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
		addedModel:             addedModel,
	})

	if err := SaveFieldPaths(config.SettingsPath, updates); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nSaved to %s\n", StderrStyles().InlineCode.Render(config.SettingsPath))

	if keyStorage == "env" && apiName != "ollama" {
		fmt.Fprintf(os.Stderr, "\nRemember to export your key:\n  export %s=sk-...\n",
			envVarName)
	}
	if webSearchOn && webSearchProviderUsesKey(webSearchProvider) && webSearchKeyStorage == "env" {
		fmt.Fprintf(os.Stderr, "\nRemember to export your web search key:\n  export %s=...\n", webSearchAPIKeyEnv)
	}

	return nil
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
	opts = append(opts, huh.NewOption("Add new OpenAI-compatible provider", addProviderOption))
	return opts
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

// buildModelOptions returns sorted model options for the given provider.
func buildModelOptions(apiName string) []huh.Option[string] {
	for _, api := range config.APIs {
		if api.Name != apiName {
			continue
		}
		names := make([]string, 0, len(api.Models))
		for name := range api.Models {
			names = append(names, name)
		}
		sort.Strings(names)
		opts := make([]huh.Option[string], 0, len(names)+1)
		for _, name := range names {
			opts = append(opts, huh.NewOption(name, name))
		}
		opts = append(opts, huh.NewOption("Add new model", addModelOption))
		return opts
	}
	return nil
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

func resolveWizardProviderModel(chosenAPI, chosenModel, newProviderName, newModelNames, baseURL string) (string, string, []string, string, bool, error) {
	apiName := wizardProviderName(chosenAPI, newProviderName)
	addedModel := chosenAPI == addProviderOption || chosenModel == addModelOption
	modelName := strings.TrimSpace(chosenModel)
	addedModelNames := []string(nil)
	if addedModel {
		var err error
		addedModelNames, err = parseNewModelNames(apiName, newModelNames)
		if err != nil {
			return "", "", nil, "", false, err
		}
		modelName = addedModelNames[0]
	}
	providerBaseURL := strings.TrimSpace(baseURL)
	if providerBaseURL == "" {
		providerBaseURL = findBaseURL(apiName)
	}
	return apiName, modelName, addedModelNames, providerBaseURL, addedModel, nil
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
	apiName, modelName, reviewMode, fsMode          string
	webSearchProvider, webSearchProviderValue       string
	webSearchKeyStorage, webSearchAPIKey            string
	webSearchAPIKeyEnv                              string
	keyStorage, apiKey, envVarName, baseURLInput    string
	addedModelNames                                 []string
	shellOn, thinkingOn, webSearchOn, addedModel    bool
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

	if d.addedModel {
		modelNames := d.addedModelNames
		if len(modelNames) == 0 {
			modelNames = []string{d.modelName}
		}
		for _, modelName := range modelNames {
			modelName = strings.TrimSpace(modelName)
			if modelName == "" {
				continue
			}
			updates = append(updates, FieldUpdate{
				Path:  []string{"apis", d.apiName, "models", modelName, "max-input-chars"},
				Value: defaultNewModelInputChars,
			})
		}
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
	case "anthropic", "google", "cohere", "ollama", "azure":
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

type summaryData struct {
	api, model, keyStorage, envVarName, baseURL string
	fsMode                                      string
	addedModelCount                             int
	shellOn, thinkingOn, webSearchOn            bool
	webSearchProvider, webSearchKeyStorage      string
	webSearchAPIKeyEnv                          string
	reviewMode, settingsPath                    string
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
