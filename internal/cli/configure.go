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
)

// RunConfigWizard launches an interactive TUI that guides the user through
// the essential mods setup: provider, model, API key, built-in tools, and
// review mode. Results are saved to the config file via yaml.Node round-trip,
// preserving existing comments.
func RunConfigWizard() error {
	// Pre-fill with current config values.
	chosenAPI := config.API
	chosenModel := config.Model
	var apiKey, keyStorage, baseURL string
	fsMode := string(config.BuiltinTools.Filesystem)
	if fsMode == "" {
		fsMode = "auto"
	}
	shellOn := config.BuiltinTools.Shell
	thinkingOn := config.BuiltinTools.SequentialThinking
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
		// Page 1: Provider + Model
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose your AI provider").
				Description("Pick the provider whose API you want to use.").
				Options(providerOpts...).
				Value(&chosenAPI),
			huh.NewSelect[string]().
				TitleFunc(func() string {
					return fmt.Sprintf("Default model for %s", chosenAPI)
				}, &chosenAPI).
				OptionsFunc(func() []huh.Option[string] {
					return buildModelOptions(chosenAPI)
				}, &chosenAPI).
				Value(&chosenModel),
		),

		// Page 2: API key storage method (skip for ollama)
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("How do you want to provide your API key?").
				Options(
					huh.NewOption("Use environment variable (recommended)", "env"),
					huh.NewOption("Save in config file", "config"),
				).
				Value(&keyStorage),
		).WithHideFunc(func() bool { return chosenAPI == "ollama" }),

		// Page 3: API key input (skip for ollama or env-var storage)
		huh.NewGroup(
			huh.NewInput().
				Title("Enter your API key").
				Description("The key is stored in plaintext in your config file.").
				Placeholder("sk-...").
				Password(true).
				Value(&apiKey),
		).WithHideFunc(func() bool { return chosenAPI == "ollama" || keyStorage != "config" }),

		// Page 4: Base URL (only for custom provider)
		huh.NewGroup(
			huh.NewInput().
				Title("Base URL for your custom API").
				Description("Any OpenAI-compatible endpoint.").
				Placeholder("https://your-server.com/v1").
				Value(&baseURL),
		).WithHideFunc(func() bool { return chosenAPI != "custom" }),

		// Page 5: Built-in tools
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Filesystem tools (read/write files)").
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
		),

		// Page 6: Review mode
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Review mode for tool execution").
				Options(
					huh.NewOption("Mutable — review risky actions (default)", "mutable"),
					huh.NewOption("Always — review every tool call", "always"),
					huh.NewOption("Never — no review (automation only)", "never"),
				).
				Value(&reviewMode),
		),
	).
		WithTheme(themeFrom(config.Theme)).
		WithKeyMap(keymap)

	if err := form.Run(); err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, "\nCanceled.")
			return nil
		}
		return fmt.Errorf("config wizard: %w", err)
	}

	// Resolve the env var name for the chosen provider (for display + save).
	envVarName := resolveEnvVar(chosenAPI)
	providerBaseURL := findBaseURL(chosenAPI)
	if baseURL != "" {
		providerBaseURL = baseURL
	}

	// Connection test (OpenAI-compatible providers only, with a key to test).
	if apiKey != "" && isOpenAICompatible(chosenAPI) && providerBaseURL != "" {
		fmt.Fprintf(os.Stderr, "\nTesting connection to %s... ", chosenAPI)
		if err := testConnection(chosenModel, providerBaseURL, apiKey); err != nil {
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
		api:            chosenAPI,
		model:          chosenModel,
		keyStorage:     keyStorage,
		envVarName:     envVarName,
		baseURL:        baseURL,
		fsMode:         fsMode,
		shellOn:        shellOn,
		thinkingOn:     thinkingOn,
		reviewMode:     reviewMode,
		settingsPath:   config.SettingsPath,
	})

	// Build updates and save.
	updates := map[string]any{
		"default-api":                     chosenAPI,
		"default-model":                   chosenModel,
		"review-mode":                     reviewMode,
		"builtin-tools.filesystem":        fsMode,
		"builtin-tools.shell":             shellOn,
		"builtin-tools.sequential-thinking": thinkingOn,
	}

	if chosenAPI != "ollama" {
		if keyStorage == "config" && apiKey != "" {
			updates["apis."+chosenAPI+".api-key"] = apiKey
		} else if envVarName != "" {
			updates["apis."+chosenAPI+".api-key-env"] = envVarName
		}
	}

	if baseURL != "" {
		updates["apis."+chosenAPI+".base-url"] = baseURL
	}

	if err := SaveFields(config.SettingsPath, updates); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "\nSaved to %s\n", StderrStyles().InlineCode.Render(config.SettingsPath))

	if keyStorage == "env" && chosenAPI != "ollama" {
		fmt.Fprintf(os.Stderr, "\nRemember to export your key:\n  export %s=sk-...\n",
			envVarName)
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

	opts := make([]huh.Option[string], 0, len(config.APIs))
	for _, api := range config.APIs {
		label := api.Name
		if desc, ok := descs[api.Name]; ok {
			label = fmt.Sprintf("%-12s  %s", api.Name, desc)
		}
		opts = append(opts, huh.NewOption(label, api.Name))
	}
	return opts
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
		opts := make([]huh.Option[string], 0, len(names))
		for _, name := range names {
			opts = append(opts, huh.NewOption(name, name))
		}
		return opts
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
		"model":       model,
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
		"max_tokens":  5,
		"stream":      false,
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
	defer resp.Body.Close()

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
	shellOn, thinkingOn                         bool
	reviewMode, settingsPath                    string
}

func printConfigSummary(d summaryData) {
	var b strings.Builder
	b.WriteString("\n  Configuration summary:\n")
	fmt.Fprintf(&b, "    Provider:    %s\n", d.api)
	fmt.Fprintf(&b, "    Model:       %s\n", d.model)

	if d.api != "ollama" {
		if d.keyStorage == "config" {
			b.WriteString("    API key:     saved in config\n")
		} else {
			fmt.Fprintf(&b, "    API key:     env var %s\n", d.envVarName)
		}
	}
	if d.baseURL != "" {
		fmt.Fprintf(&b, "    Base URL:    %s\n", d.baseURL)
	}

	fmt.Fprintf(&b, "    Filesystem:  %s\n", d.fsMode)
	fmt.Fprintf(&b, "    Shell:       %v\n", d.shellOn)
	fmt.Fprintf(&b, "    Thinking:    %v\n", d.thinkingOn)
	fmt.Fprintf(&b, "    Review:      %s\n", d.reviewMode)

	fmt.Fprint(os.Stderr, b.String())
}
