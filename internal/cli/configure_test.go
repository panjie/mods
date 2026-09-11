package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestBuildProviderOptionsIncludesAddProvider(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai"}},
		},
	}, func() {
		opts := buildProviderOptions()
		require.NotEmpty(t, opts)
		require.Equal(t, addProviderOption, opts[len(opts)-1].Value)
	})
}

func TestBuildProviderOptionsGroupsConfiguredAndAvailableProviders(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{
				{
					Name: "openai",
					Models: map[string]Model{
						"gpt-5.4": {},
						"o4-mini": {},
					},
				},
				{
					Name: "custom",
					Models: map[string]Model{
						"my-model": {},
					},
				},
				{Name: "anthropic"},
				{Name: "empty-custom"},
			},
		},
	}, func() {
		opts := buildProviderOptions()

		require.Greater(t, len(opts), 4)
		require.Equal(t, "openai", opts[0].Value)
		require.Equal(t, "custom", opts[1].Value)
		require.Contains(t, opts[0].Key, "✓")
		require.NotContains(t, opts[0].Key, "Configured")
		require.Contains(t, opts[0].Key, "gpt-5.4")
		require.Contains(t, opts[0].Key, "o4-mini")
		require.NotContains(t, opts[0].Key, "OpenAI API")
		require.Contains(t, opts[1].Key, "✓")
		require.NotContains(t, opts[1].Key, "Configured")
		require.Contains(t, opts[1].Key, "my-model")

		values := make([]string, 0, len(opts))
		for _, opt := range opts {
			values = append(values, opt.Value)
		}
		require.NotContains(t, values[2:len(values)-1], "openai")
		require.Contains(t, values[2:len(values)-1], "anthropic")
		require.Contains(t, values[2:len(values)-1], "empty-custom")
		require.Contains(t, values, "github-copilot")
		anthropicLabel := providerOptionLabel(t, opts, "anthropic")
		require.Contains(t, anthropicLabel, "Anthropic API")
		require.NotContains(t, anthropicLabel, "Claude")
		emptyCustomLabel := providerOptionLabel(t, opts, "empty-custom")
		require.Contains(t, emptyCustomLabel, "+")
		require.Contains(t, emptyCustomLabel, "no models configured")
		require.Equal(t, addProviderOption, opts[len(opts)-1].Value)
		require.Contains(t, opts[len(opts)-1].Key, "Add new provider")
	})
}

func TestBuildProviderOptionsIncludesUnconfiguredBuiltInProviders(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai"}},
		},
	}, func() {
		opts := buildProviderOptions()
		values := make([]string, 0, len(opts))
		for _, opt := range opts {
			values = append(values, opt.Value)
		}
		require.Contains(t, values, "github-copilot")
		require.Equal(t, addProviderOption, opts[len(opts)-1].Value)
	})
}

func TestConfiguredProviderModelsSummarySortsAndTruncates(t *testing.T) {
	api := API{
		Name: "custom",
		Models: map[string]Model{
			"delta": {},
			"alpha": {},
			"gamma": {},
			"beta":  {},
		},
	}

	require.Equal(t, "alpha, beta, delta, +1 more", configuredProviderModelsSummary(api))
}

func providerOptionLabel(t *testing.T, options []setupOption, value string) string {
	t.Helper()
	for _, option := range options {
		if option.Value == value {
			return option.Key
		}
	}
	t.Fatalf("provider option %q not found", value)
	return ""
}

func TestDiscoverOptionsUsesCopilotProtocolForNewProviderNamedGitHubCopilot(t *testing.T) {
	require.Equal(t, "github-copilot", configWizardDiscoveryType(addProviderOption, "github-copilot", "openai"))
	require.Equal(t, "openai", configWizardDiscoveryType(addProviderOption, "groq", "openai"))
}

func TestConfigWizardManualModelsDescriptionShowsDiscoveryFailure(t *testing.T) {
	require.NotContains(t, configWizardManualModelsDescription(nil), "failed")
	require.Contains(t, configWizardManualModelsDescription(fmt.Errorf("network unavailable")),
		"Model discovery failed: network unavailable")
}

func TestConfigWizardDiscoversCopilotOnlyAfterAuthToken(t *testing.T) {
	require.True(t, configWizardWaitingForCopilotAuth("github-copilot", ""),
		"Copilot model discovery must wait for device auth")

	require.False(t, configWizardWaitingForCopilotAuth("github-copilot", "fresh-github-oauth-token"),
		"Copilot model discovery can run after device auth")
	require.False(t, configWizardWaitingForCopilotAuth("openai", ""),
		"non-Copilot providers do not need Copilot device auth")
}

func TestConfigWizardUsesExistingCopilotToken(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{Name: "github-copilot", APIKey: "saved-github-oauth-token"}},
	}}, func() {
		require.False(t, configWizardWaitingForCopilotAuth("github-copilot", ""),
			"configured Copilot auth token should not require device auth again")
	})
}

func TestNormalizeWebSearchProviderForWizard(t *testing.T) {
	tests := map[string]string{
		"":          "tavily",
		"tavily":    "tavily",
		"custom":    "custom",
		"https://x": "custom",
		"unknown":   "tavily",
	}
	for input, expected := range tests {
		t.Run(input, func(t *testing.T) {
			require.Equal(t, expected, normalizeWebSearchProviderForWizard(input))
		})
	}
}

func TestBuildConfigWizardUpdatesNewProviderSavesBaseURLAndModels(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "groq",
		modelName:              "llama-3.3-70b-versatile",
		reviewMode:             "auto",
		fsMode:                 "auto",
		webSearchProvider:      "tavily",
		webSearchProviderValue: "tavily",
		keyStorage:             "env",
		envVarName:             "GROQ_API_KEY",
		baseURLInput:           " https://api.groq.com/openai/v1 ",
		addedModelNames:        []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
	})

	requireUpdateValue(t, updates, []string{"apis", "groq", "base-url"}, "https://api.groq.com/openai/v1")
	requireUpdateValue(t, updates, []string{"apis", "groq", "api-key-env"}, "GROQ_API_KEY")
	requireUpdateValue(t, updates, []string{"default-model"}, "llama-3.3-70b-versatile")
	requireUpdateValue(t, updates, []string{"apis", "groq", "models", "llama-3.3-70b-versatile"}, map[string]any{})
	requireUpdateValue(t, updates, []string{"apis", "groq", "models", "llama-3.1-8b-instant"}, map[string]any{})

	path := writeCLIConfig(t, `default-api: openai
default-model: gpt-5.4
apis: {}
`)
	require.NoError(t, SaveFieldPaths(path, updates))

	m := loadCLIConfig(t, path)
	apis := m["apis"].(map[string]any)
	groq := apis["groq"].(map[string]any)
	require.Equal(t, "https://api.groq.com/openai/v1", groq["base-url"])
	require.Equal(t, "GROQ_API_KEY", groq["api-key-env"])
	models := groq["models"].(map[string]any)
	// Each newly added model is registered as an empty mapping so the model is
	// selectable while leaving every optional per-model field unset.
	model := models["llama-3.3-70b-versatile"].(map[string]any)
	require.Empty(t, model)
	model = models["llama-3.1-8b-instant"].(map[string]any)
	require.Empty(t, model)
}

func TestBuildConfigWizardUpdatesDeepSeekFlashUsesResponses(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "deepseek",
		modelName:              "deepseek-v4-flash",
		reviewMode:             "auto",
		fsMode:                 "auto",
		webSearchProvider:      "tavily",
		webSearchProviderValue: "tavily",
		keyStorage:             "env",
		envVarName:             "DEEPSEEK_API_KEY",
		baseURLInput:           "https://api.deepseek.com/",
		addedModelNames:        []string{"deepseek-v4-flash"},
	})

	requireUpdateValue(t, updates, []string{"apis", "deepseek", "models", "deepseek-v4-flash"}, map[string]any{
		"endpoint": "responses",
	})
	requireNoUpdatePath(t, updates, []string{"apis", "deepseek", "models", "deepseek-v4-flash", "thinking-type"})

	path := writeCLIConfig(t, `default-api: openai
apis:
  deepseek:
    base-url: https://api.deepseek.com/
`)
	require.NoError(t, SaveFieldPaths(path, updates))
	saved := loadCLIConfig(t, path)
	deepseek := saved["apis"].(map[string]any)["deepseek"].(map[string]any)
	model := deepseek["models"].(map[string]any)["deepseek-v4-flash"].(map[string]any)
	require.Equal(t, "responses", model["endpoint"])
}

func TestBuildConfigWizardUpdatesDeepSeekProUsesResponses(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "deepseek",
		modelName:              "deepseek-v4-pro",
		reviewMode:             "auto",
		fsMode:                 "auto",
		webSearchProvider:      "tavily",
		webSearchProviderValue: "tavily",
		keyStorage:             "env",
		envVarName:             "DEEPSEEK_API_KEY",
		baseURLInput:           "https://api.deepseek.com/",
		addedModelNames:        []string{"deepseek-v4-pro"},
	})

	requireUpdateValue(t, updates, []string{"apis", "deepseek", "models", "deepseek-v4-pro"}, map[string]any{
		"endpoint": "responses",
	})

	path := writeCLIConfig(t, `default-api: openai
apis:
  deepseek:
    base-url: https://api.deepseek.com/
`)
	require.NoError(t, SaveFieldPaths(path, updates))
	saved := loadCLIConfig(t, path)
	deepseek := saved["apis"].(map[string]any)["deepseek"].(map[string]any)
	model := deepseek["models"].(map[string]any)["deepseek-v4-pro"].(map[string]any)
	require.Equal(t, "responses", model["endpoint"])
}

func TestBuildConfigWizardUpdatesDeepSeekCustomGatewayKeepsDefaultEndpoint(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:         "deepseek",
		modelName:       "deepseek-v4-flash",
		baseURLInput:    "https://gateway.example.com/v1",
		addedModelNames: []string{"deepseek-v4-flash"},
	})

	requireUpdateValue(t, updates, []string{"apis", "deepseek", "models", "deepseek-v4-flash"}, map[string]any{})
}

func TestBuildConfigWizardUpdatesUsesExplicitDefaultModel(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:         "qwen",
		modelName:       "qwen3.6-flash",
		addedModelNames: []string{"qwen3.6-35b-a3b", "qwen3.6-flash"},
	})

	requireUpdateValue(t, updates, []string{"default-model"}, "qwen3.6-flash")
}

func TestBuildConfigWizardUpdatesGitHubCopilotStoresDeviceTokenInConfig(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "github-copilot",
		modelName:              "gpt-5",
		reviewMode:             "auto",
		fsMode:                 "auto",
		webSearchProvider:      "tavily",
		webSearchProviderValue: "tavily",
		keyStorage:             "config",
		apiKey:                 "github-oauth-token",
		baseURLInput:           "https://api.githubcopilot.com",
		addedModelNames:        []string{"gpt-5", "claude-sonnet-4"},
		copilotModelEndpoints:  map[string]string{"gpt-5": "responses", "claude-sonnet-4": "messages"},
	})

	requireUpdateValue(t, updates, []string{"apis", "github-copilot", "api-key"}, "github-oauth-token")
	requireUpdateValue(t, updates, []string{"apis", "github-copilot", "api-key-env"}, nil)
	requireUpdateValue(t, updates, []string{"apis", "github-copilot", "models", "gpt-5"}, map[string]any{"endpoint": "responses"})
	requireUpdateValue(t, updates, []string{"apis", "github-copilot", "models", "claude-sonnet-4"}, map[string]any{"endpoint": "messages"})
}

func TestBuildConfigWizardUpdatesGitHubCopilotKeepsExistingConfigToken(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{Name: "github-copilot", APIKey: "saved-github-oauth-token"}},
	}}, func() {
		updates := buildConfigWizardUpdates(configWizardSaveData{
			apiName:                "github-copilot",
			modelName:              "gpt-5",
			reviewMode:             "auto",
			fsMode:                 "auto",
			webSearchProvider:      "tavily",
			webSearchProviderValue: "tavily",
			keyStorage:             "env",
			envVarName:             "GITHUB_COPILOT_API_KEY",
			addedModelNames:        []string{"gpt-5"},
		})

		requireUpdateValue(t, updates, []string{"apis", "github-copilot", "api-key"}, "saved-github-oauth-token")
		requireUpdateValue(t, updates, []string{"apis", "github-copilot", "api-key-env"}, nil)
	})
}

func TestConfigWizardNewModelNamesOnlyCountsNewUniqueModels(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name: "openrouter",
			Models: map[string]Model{
				"existing": {},
			},
		}},
	}}, func() {
		require.Equal(t,
			[]string{"new-a", "new-b"},
			configWizardNewModelNames("openrouter", []string{
				"existing", " new-a ", "", "new-a", "new-b",
			}),
		)
	})
}

func TestBuildConfigWizardUpdatesWritesAPITypeForAnthropic(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:         "acme-claude",
		apiType:         "anthropic",
		modelName:       "claude-sonnet-4",
		reviewMode:      "auto",
		fsMode:          "auto",
		keyStorage:      "env",
		envVarName:      "ACME_CLAUDE_API_KEY",
		baseURLInput:    "https://acme.example.com/v1",
		addedModelNames: []string{"claude-sonnet-4"},
	})

	requireUpdateValue(t, updates, []string{"apis", "acme-claude", "api-type"}, "anthropic")

	path := writeCLIConfig(t, "default-api: openai\ndefault-model: m\napis: {}\n")
	require.NoError(t, SaveFieldPaths(path, updates))
	m := loadCLIConfig(t, path)
	apis := m["apis"].(map[string]any)
	require.Equal(t, "anthropic", apis["acme-claude"].(map[string]any)["api-type"])
}

func TestBuildConfigWizardUpdatesOmitsAPITypeForOpenAI(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:         "groq",
		apiType:         "openai",
		modelName:       "llama",
		reviewMode:      "auto",
		fsMode:          "auto",
		keyStorage:      "env",
		envVarName:      "GROQ_API_KEY",
		addedModelNames: []string{"llama"},
	})
	requireNoUpdatePath(t, updates, []string{"apis", "groq", "api-type"})
}

func TestBuildConfigWizardUpdatesExistingProviderDoesNotRewriteBaseURL(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "openrouter",
		modelName:              "vendor/gpt-5.5:latest",
		reviewMode:             "auto",
		fsMode:                 "auto",
		webSearchProvider:      "tavily",
		webSearchProviderValue: "tavily",
		keyStorage:             "env",
		envVarName:             "OPENROUTER_API_KEY",
		addedModelNames:        []string{"vendor/gpt-5.5:latest"},
	})

	requireNoUpdatePath(t, updates, []string{"apis", "openrouter", "base-url"})
	requireNoUpdatePath(t, updates, []string{"apis", "openrouter", "api-type"})
	requireUpdateValue(t, updates, []string{"apis", "openrouter", "models", "vendor/gpt-5.5:latest"}, map[string]any{})
}

// TestSeedThenSaveBootstrapsPortableConfig exercises the seed-then-save
// sequence the wizard uses when the user picks a config location whose file
// does not yet exist (the portable bootstrap case). SaveFieldPaths is a
// round-trip update and errors on a missing file, so WriteDefaultFile must
// seed the target first.
func TestSeedThenSaveBootstrapsPortableConfig(t *testing.T) {
	// Target file does not exist yet, mirroring a fresh <exeDir>/mods.yml.
	path := filepath.Join(t.TempDir(), "mods.yml")
	_, statErr := os.Stat(path)
	require.ErrorIs(t, statErr, os.ErrNotExist)

	// SaveFieldPaths alone would fail (no file to read).
	updates := []FieldUpdate{
		{Path: []string{"default-api"}, Value: "groq"},
		{Path: []string{"default-model"}, Value: "llama-3.3-70b-versatile"},
	}
	require.Error(t, SaveFieldPaths(path, updates))

	// Seed first, then save — the wizard's actual sequence.
	require.NoError(t, WriteDefaultFile(path))
	require.FileExists(t, path)
	require.NoError(t, SaveFieldPaths(path, updates))

	m := loadCLIConfig(t, path)
	require.Equal(t, "groq", m["default-api"])
	require.Equal(t, "llama-3.3-70b-versatile", m["default-model"])
}

func TestSaveConfigWizardMigratesPortableConfigToStandard(t *testing.T) {
	portableDir := t.TempDir()
	portablePath := filepath.Join(portableDir, "mods.yml")
	require.NoError(t, os.WriteFile(portablePath, []byte("default-api: ollama\n"), 0o600))
	standardPath := filepath.Join(t.TempDir(), "mods", "mods.yml")

	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name:   "ollama",
				Models: map[string]Model{"llama3.1": {}},
			}},
		},
		SettingsPath:    portablePath,
		SettingsExisted: true,
		PortableDir:     portableDir,
	}, func() {
		output := captureStderr(t, func() {
			err := writeSetupConfig(standardPath, portablePath, configWizardSaveData{
				apiName:                "ollama",
				modelName:              "llama3.1",
				reviewMode:             "auto",
				fsMode:                 "auto",
				webSearchProviderValue: "tavily",
				addedModelNames:        []string{"llama3.1"},
				portable:               false,
			})
			require.NoError(t, err)
		})

		require.FileExists(t, standardPath)
		_, err := os.Stat(portablePath)
		require.ErrorIs(t, err, os.ErrNotExist,
			"the old portable file must be removed or it will keep taking precedence")
		require.Empty(t, output, "persistence must not print while the TUI owns stderr")
	})
}

func TestValidateNewProviderName(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai"}},
		},
	}, func() {
		require.NoError(t, validateNewProviderName("groq_1"))
		require.Error(t, validateNewProviderName(""))
		require.Error(t, validateNewProviderName("Groq"))
		require.Error(t, validateNewProviderName("openai"))
	})
}

func TestValidateNewModelName(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name: "openrouter",
				Models: map[string]Model{
					"anthropic/claude-sonnet-4-6": {},
				},
			}},
		},
	}, func() {
		require.NoError(t, validateNewModelName("openrouter", "vendor/gpt-5.5:latest"))
		require.Error(t, validateNewModelName("openrouter", ""))
		require.Error(t, validateNewModelName("openrouter", "anthropic/claude-sonnet-4-6"))
	})
}

func TestConfigWizardModelNamesTrimsSkipsEmptyAndDeduplicates(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name: "openrouter",
				Models: map[string]Model{
					"anthropic/claude-sonnet-4-6": {},
				},
			}},
		},
	}, func() {
		models, err := configWizardModelNames("openrouter", nil, "\n vendor/gpt-5.5:latest \n\nvendor/gpt-5.5:latest\nopenai/gpt-5.4\n")
		require.NoError(t, err)
		require.Equal(t, []string{"vendor/gpt-5.5:latest", "openai/gpt-5.4"}, models)

		_, err = configWizardModelNames("openrouter", nil, "\n \t")
		require.Error(t, err)

		// The wizard accepts a model that is already configured: re-running
		// --config on the same provider must not reject the existing entries.
		models, err = configWizardModelNames("openrouter", nil, "anthropic/claude-sonnet-4-6")
		require.NoError(t, err)
		require.Equal(t, []string{"anthropic/claude-sonnet-4-6"}, models)

		// A successful discovery selection takes precedence over manual text.
		models, err = configWizardModelNames("openrouter", []string{"picked/model"}, "manual/model")
		require.NoError(t, err)
		require.Equal(t, []string{"picked/model"}, models)
	})
}

func TestConfigWizardModelNamesUsesDiscoveredSelection(t *testing.T) {
	models, err := configWizardModelNames("openai", []string{"gpt-5.4", "gpt-5.4-mini"}, "manual-model")

	require.NoError(t, err)
	require.Equal(t, []string{"gpt-5.4", "gpt-5.4-mini"}, models)
}

func TestConfigWizardModelNamesDoesNotAddUnselectedConfiguredModels(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name: "qwen",
			Models: map[string]Model{
				"configured-only": {},
			},
		}},
	}}, func() {
		models, err := configWizardModelNames("qwen", []string{"selected-model"}, "")

		require.NoError(t, err)
		require.Equal(t, []string{"selected-model"}, models)
	})
}

func TestConfigWizardModelOptionLabelMarksOnlyCurrentProviderDefault(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		API:   "qwen",
		Model: "qwen3.6-flash",
	}}, func() {
		require.Equal(t,
			"qwen3.6-flash (current default)",
			configWizardModelOptionLabel("qwen", "qwen3.6-flash"),
		)
		require.Equal(t,
			"qwen3.6-flash",
			configWizardModelOptionLabel("other", "qwen3.6-flash"),
		)
		require.Equal(t,
			"qwen3.6-35b-a3b",
			configWizardModelOptionLabel("qwen", "qwen3.6-35b-a3b"),
		)
	})
}

func TestConfigWizardPreferredDefaultModelPreservesOrFallsBack(t *testing.T) {
	models := []string{"first", "current", "last"}

	require.Equal(t, "current", configWizardPreferredDefaultModel("current", models))
	require.Equal(t, "first", configWizardPreferredDefaultModel("removed", models))
	require.Empty(t, configWizardPreferredDefaultModel("removed", nil))
}

func TestValidateConfigWizardDefaultModelRequiresCandidate(t *testing.T) {
	models := []string{"first", "chosen"}

	require.NoError(t, validateConfigWizardDefaultModel("chosen", models))
	require.Error(t, validateConfigWizardDefaultModel("configured-only", models))
	require.Error(t, validateConfigWizardDefaultModel("", models))
}

func TestConfigWizardModelNamesRequiresManualWhenNoDiscoverySelection(t *testing.T) {
	models, err := configWizardModelNames("openai", nil, "\n manual-model \n")

	require.NoError(t, err)
	require.Equal(t, []string{"manual-model"}, models)

	_, err = configWizardModelNames("openai", nil, "\n\t")
	require.Error(t, err)
}

func TestConfigWizardModelNamesAllowsConfiguredManualModels(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name: "google",
			Models: map[string]Model{
				"gemini-2.5-flash": {},
			},
		}},
	}}, func() {
		models, err := configWizardModelNames("google", nil, "gemini-2.5-flash\ngemini-2.5-pro")

		require.NoError(t, err)
		require.Equal(t, []string{"gemini-2.5-flash", "gemini-2.5-pro"}, models)
	})
}

func TestConfigWizardPreselectsConfiguredDiscoveredModels(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name: "openai",
			Models: map[string]Model{
				"gpt-5.4":         {},
				"configured-only": {},
			},
		}},
	}}, func() {
		catalog := newConfigWizardProviderCatalog(config)
		got := catalog.preselectedDiscoveredModels("openai", []string{"gpt-5.4-mini", "gpt-5.4", "gpt-5.5"})

		require.Equal(t, []string{"gpt-5.4"}, got)
	})
}

func TestManualModelTextForProviderUsesConfiguredModels(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name: "google",
			Models: map[string]Model{
				"gemini-2.5-pro":   {},
				"gemini-2.5-flash": {},
			},
		}},
	}}, func() {
		catalog := newConfigWizardProviderCatalog(config)
		got := catalog.manualModelText("google")

		require.Equal(t, "gemini-2.5-flash\ngemini-2.5-pro", got)
	})
}

func TestValidateWizardBaseURLRequiresNewProviderURL(t *testing.T) {
	require.Error(t, validateWizardBaseURL(addProviderOption, ""))
	require.NoError(t, validateWizardBaseURL("custom", ""))
	require.Error(t, validateWizardBaseURL(addProviderOption, "api.groq.com/openai/v1"))
	require.NoError(t, validateWizardBaseURL(addProviderOption, "https://api.groq.com/openai/v1"))
}

func requireUpdateValue(t *testing.T, updates []FieldUpdate, path []string, value any) {
	t.Helper()
	for _, update := range updates {
		if equalPath(update.Path, path) {
			require.Equal(t, value, update.Value)
			return
		}
	}
	require.Failf(t, "missing update", "path %v was not updated", path)
}

func requireNoUpdatePath(t *testing.T, updates []FieldUpdate, path []string) {
	t.Helper()
	for _, update := range updates {
		require.Falsef(t, equalPath(update.Path, path), "unexpected update for path %v", path)
	}
}

func equalPath(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func writeCLIConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func loadCLIConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	reader, writer, err := os.Pipe()
	require.NoError(t, err)
	os.Stderr = writer
	defer func() {
		os.Stderr = old
	}()

	fn()

	require.NoError(t, writer.Close())
	out, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	return string(out)
}

func TestDiscoverModelsOpenAI(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-4o"}, {"id": "gpt-4o-mini"}},
		})
	}))
	defer srv.Close()

	ids, err := discoverModels("openai", srv.URL+"/v1", "sk-test")
	require.NoError(t, err)
	require.Equal(t, []string{"gpt-4o", "gpt-4o-mini"}, ids)
}

func TestDiscoverModelsAnthropic(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/models", r.URL.Path)
		require.Equal(t, "sk-test", r.Header.Get("x-api-key"))
		require.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "claude-sonnet-4"}, {"id": "claude-haiku-4"}},
		})
	}))
	defer srv.Close()

	// base-url with the full messages endpoint is normalized away.
	ids, err := discoverModels("anthropic", srv.URL+"/v1/messages", "sk-test")
	require.NoError(t, err)
	require.Equal(t, []string{"claude-haiku-4", "claude-sonnet-4"}, ids) // sorted
}

func TestDiscoverModelsAnthropicFallsBackToOpenAIStyleModels(t *testing.T) {
	hits := map[string]int{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits[r.URL.Path]++
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		// /models — OpenAI-style list, same x-api-key auth.
		require.Equal(t, "sk-test", r.Header.Get("x-api-key"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "glm-4.6"}},
		})
	}))
	defer srv.Close()

	ids, err := discoverModels("anthropic", srv.URL, "sk-test")
	require.NoError(t, err)
	require.Equal(t, []string{"glm-4.6"}, ids)
	require.Equal(t, 1, hits["/v1/models"])
	require.Equal(t, 1, hits["/models"])
}

func TestDiscoverModelsOllama(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/tags", r.URL.Path)
		require.Empty(t, r.Header.Get("Authorization"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]string{{"name": "llama3.1:latest"}, {"name": "qwen2.5:7b"}},
		})
	}))
	defer srv.Close()

	ids, err := discoverModels("ollama", srv.URL, "")
	require.NoError(t, err)
	require.Equal(t, []string{"llama3.1:latest", "qwen2.5:7b"}, ids)
}

func TestDiscoverModelsGoogle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("key"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name":                       "models/gemini-2.5-pro",
					"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
				},
				{
					"name":                       "models/gemini-2.5-flash",
					"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
				},
				{
					"name":                       "models/text-embedding-004",
					"supportedGenerationMethods": []string{"embedContent"},
				},
				{
					"name":                       "models/gemini-2.0-flash",
					"supportedGenerationMethods": []string{"generateContent", "streamGenerateContent"},
				},
			},
		})
	}))
	defer srv.Close()

	ids, err := discoverModels("google", srv.URL+"/v1beta", "test-key")
	require.NoError(t, err)
	require.Equal(t, []string{"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-pro"}, ids)
}

func TestDiscoverModelsGitHubCopilot(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			require.Equal(t, "Bearer github-oauth-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "copilot-token"})
		case "/models":
			require.Equal(t, "Bearer copilot-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "gpt-5", "supported_endpoints": []string{"/responses"}},
					{"id": "claude-sonnet-4", "supported_endpoints": []string{"/v1/messages"}},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	oldGitHubAPIBaseURL := copilotGitHubAPIBaseURL
	defer func() { copilotGitHubAPIBaseURL = oldGitHubAPIBaseURL }()
	copilotGitHubAPIBaseURL = srv.URL

	ids, err := discoverModels("github-copilot", srv.URL, "github-oauth-token")
	require.NoError(t, err)
	require.Equal(t, []string{"claude-sonnet-4", "gpt-5"}, ids)
}

func TestDiscoverModelsGoogleFiltersNonGenerative(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"models": []map[string]any{
				{
					"name":                       "models/text-embedding-004",
					"supportedGenerationMethods": []string{"embedContent"},
				},
				{
					"name":                       "models/aqa",
					"supportedGenerationMethods": []string{"generateAnswer"},
				},
			},
		})
	}))
	defer srv.Close()

	_, err := discoverModels("google", srv.URL, "test-key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no generative models")
}

func TestModelDiscoveryNetworkErrorRedactsGoogleAPIKey(t *testing.T) {
	err := modelDiscoveryNetworkError(&url.Error{
		Op:  "Get",
		URL: "https://generativelanguage.googleapis.com/v1beta/models?key=secret-key",
		Err: fmt.Errorf("socket is not connected"),
	})

	require.EqualError(t, err, "network error: socket is not connected")
	require.NotContains(t, err.Error(), "secret-key")
}

func TestBuiltinBaseURL(t *testing.T) {
	cases := []struct {
		name    string
		apiName string
		want    string
	}{
		{"google", "google", "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse"},
		{"github-copilot", "github-copilot", "https://api.githubcopilot.com"},
		{"openai", "openai", "https://api.openai.com/v1"},
		{"anthropic", "anthropic", "https://api.anthropic.com/v1"},
		{"ollama", "ollama", "http://localhost:11434"},
		{"unknown", "acme", "https://your-server.com/v1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, builtinBaseURL(c.apiName))
		})
	}
}

func TestGoogleListModelsBase(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty uses default", "", "https://generativelanguage.googleapis.com/v1beta"},
		{"bare root unchanged", "https://generativelanguage.googleapis.com/v1beta", "https://generativelanguage.googleapis.com/v1beta"},
		{"full stream endpoint stripped", "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse", "https://generativelanguage.googleapis.com/v1beta"},
		{"concrete model endpoint stripped", "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse", "https://generativelanguage.googleapis.com/v1beta"},
		{"trailing /models stripped", "https://generativelanguage.googleapis.com/v1beta/models", "https://generativelanguage.googleapis.com/v1beta"},
		{"custom proxy preserved", "https://my-vertex-proxy.example.com/v1beta", "https://my-vertex-proxy.example.com/v1beta"},
		{"custom proxy with model path stripped", "https://my-vertex-proxy.example.com/v1beta/models/{model}:streamGenerateContent", "https://my-vertex-proxy.example.com/v1beta"},
		{"whitespace trimmed", "  https://generativelanguage.googleapis.com/v1beta  ", "https://generativelanguage.googleapis.com/v1beta"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, googleListModelsBase(c.in))
		})
	}
}

func TestDiscoverModelsAuthError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	_, err := discoverModels("openai", srv.URL+"/v1", "bad-key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid API key")
}

func TestDiscoverModelsNoModelsReturned(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{}) // no data/models
	}))
	defer srv.Close()

	_, err := discoverModels("openai", srv.URL+"/v1", "sk-test")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no models")
}

func TestResolveKeyForDiscovery(t *testing.T) {
	t.Run("entered key wins", func(t *testing.T) {
		withTestConfig(t, Config{PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai", APIKey: "cfg-key", APIKeyEnv: "OPENAI_API_KEY"}},
		}}, func() {
			require.Equal(t, "entered", resolveKeyForDiscovery("openai", "entered"))
		})
	})

	t.Run("falls back to configured api-key", func(t *testing.T) {
		withTestConfig(t, Config{PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openai", APIKey: "cfg-key"}},
		}}, func() {
			require.Equal(t, "cfg-key", resolveKeyForDiscovery("openai", ""))
		})
	})

	t.Run("falls back to env var", func(t *testing.T) {
		t.Setenv("CUSTOM_API_KEY", "env-key")
		withTestConfig(t, Config{PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "custom", APIKeyEnv: "CUSTOM_API_KEY"}},
		}}, func() {
			require.Equal(t, "env-key", resolveKeyForDiscovery("custom", ""))
		})
	})

	t.Run("falls back to inferred env var before first save", func(t *testing.T) {
		t.Setenv("GOOGLE_API_KEY", "google-env-key")
		withTestConfig(t, Config{PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "google"}},
		}}, func() {
			require.Equal(t, "google-env-key", resolveKeyForDiscovery("google", ""))
		})
	})

	t.Run("empty when nothing configured", func(t *testing.T) {
		withTestConfig(t, Config{PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "custom"}},
		}}, func() {
			require.Empty(t, resolveKeyForDiscovery("custom", ""))
		})
	})
}

func TestFindAPIType(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{
			{Name: "opencode", APIType: "anthropic"},
			{Name: "groq"}, // no api-type
		},
	}}, func() {
		require.Equal(t, "anthropic", findAPIType("opencode"))
		require.Empty(t, findAPIType("groq"), "unset api-type should be empty")
		require.Empty(t, findAPIType("unknown"))
	})
}

func TestExistingModelNames(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name:   "openai",
			Models: map[string]Model{"gpt-4o": {}, "gpt-4o-mini": {}},
		}},
	}}, func() {
		got := existingModelNames("openai")
		require.Contains(t, got, "gpt-4o")
		require.Contains(t, got, "gpt-4o-mini")
		require.Len(t, got, 2)
		require.Empty(t, existingModelNames("unknown"))
	})
}
