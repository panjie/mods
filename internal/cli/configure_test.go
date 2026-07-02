package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
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

func TestBuildModelOptionsIncludesAddModel(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{
				Name: "openai",
				Models: map[string]Model{
					"gpt-5.5": {},
				},
			}},
		},
	}, func() {
		opts := buildModelOptions("openai")
		require.NotEmpty(t, opts)
		require.Equal(t, addModelOption, opts[len(opts)-1].Value)
	})
}

func TestConfigWizardKeyMapPrevIncludesEscAndShiftTab(t *testing.T) {
	keymap := configWizardKeyMap()
	esc := tea.KeyMsg{Type: tea.KeyEsc}
	shiftTab := tea.KeyMsg{Type: tea.KeyShiftTab}

	prevBindings := []key.Binding{
		keymap.Input.Prev,
		keymap.FilePicker.Prev,
		keymap.Text.Prev,
		keymap.Select.Prev,
		keymap.MultiSelect.Prev,
		keymap.Note.Prev,
		keymap.Confirm.Prev,
	}

	for _, binding := range prevBindings {
		require.True(t, key.Matches(esc, binding))
		require.True(t, key.Matches(shiftTab, binding))
	}
}

func TestResolveWizardProviderModel(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			APIs: []API{{Name: "openrouter", BaseURL: "https://openrouter.ai/api/v1"}},
		},
	}, func() {
		apiName, modelName, addedModelNames, baseURL, addedModel, err := resolveWizardProviderModel(
			addProviderOption,
			"",
			"groq",
			"llama-3.3-70b-versatile\nllama-3.1-8b-instant",
			"https://api.groq.com/openai/v1",
			"manual",
		)
		require.NoError(t, err)
		require.Equal(t, "groq", apiName)
		require.Equal(t, "llama-3.3-70b-versatile", modelName)
		require.Equal(t, []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"}, addedModelNames)
		require.Equal(t, "https://api.groq.com/openai/v1", baseURL)
		require.True(t, addedModel)

		apiName, modelName, addedModelNames, baseURL, addedModel, err = resolveWizardProviderModel(
			"openrouter",
			addModelOption,
			"",
			"vendor/gpt-5.5:latest",
			"",
			"manual",
		)
		require.NoError(t, err)
		require.Equal(t, "openrouter", apiName)
		require.Equal(t, "vendor/gpt-5.5:latest", modelName)
		require.Equal(t, []string{"vendor/gpt-5.5:latest"}, addedModelNames)
		require.Equal(t, "https://openrouter.ai/api/v1", baseURL)
		require.True(t, addedModel)

		apiName, modelName, addedModelNames, baseURL, addedModel, err = resolveWizardProviderModel(
			"openrouter",
			"anthropic/claude-sonnet-4-6",
			"",
			"",
			"",
			"manual",
		)
		require.NoError(t, err)
		require.Equal(t, "openrouter", apiName)
		require.Equal(t, "anthropic/claude-sonnet-4-6", modelName)
		require.Nil(t, addedModelNames)
		require.Equal(t, "https://openrouter.ai/api/v1", baseURL)
		require.False(t, addedModel)

		// Discovery skips manual parsing; models are filled in later by the
		// post-form discovery flow, so names stay empty and addedModel is true.
		apiName, modelName, addedModelNames, baseURL, addedModel, err = resolveWizardProviderModel(
			addProviderOption,
			"",
			"acme",
			"",
			"https://acme.example.com/v1",
			"discover",
		)
		require.NoError(t, err)
		require.Equal(t, "acme", apiName)
		require.Empty(t, modelName)
		require.Nil(t, addedModelNames)
		require.Equal(t, "https://acme.example.com/v1", baseURL)
		require.True(t, addedModel)
	})
}

func TestBuildConfigWizardUpdatesNewProviderSavesBaseURLAndModels(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "groq",
		modelName:              "llama-3.3-70b-versatile",
		reviewMode:             "mutable",
		fsMode:                 "auto",
		webSearchProvider:      "duckduckgo",
		webSearchProviderValue: "duckduckgo",
		keyStorage:             "env",
		envVarName:             "GROQ_API_KEY",
		baseURLInput:           " https://api.groq.com/openai/v1 ",
		addedModelNames:        []string{"llama-3.3-70b-versatile", "llama-3.1-8b-instant"},
		addedModel:             true,
	})

	requireUpdateValue(t, updates, []string{"apis", "groq", "base-url"}, "https://api.groq.com/openai/v1")
	requireUpdateValue(t, updates, []string{"apis", "groq", "api-key-env"}, "GROQ_API_KEY")
	requireUpdateValue(t, updates, []string{"default-model"}, "llama-3.3-70b-versatile")
	requireUpdateValue(t, updates, []string{"apis", "groq", "models", "llama-3.3-70b-versatile", "max-input-chars"}, defaultNewModelInputChars)
	requireUpdateValue(t, updates, []string{"apis", "groq", "models", "llama-3.1-8b-instant", "max-input-chars"}, defaultNewModelInputChars)

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
	model := models["llama-3.3-70b-versatile"].(map[string]any)
	require.Equal(t, defaultNewModelInputChars, model["max-input-chars"])
	model = models["llama-3.1-8b-instant"].(map[string]any)
	require.Equal(t, defaultNewModelInputChars, model["max-input-chars"])
}

func TestBuildConfigWizardUpdatesWritesAPITypeForAnthropic(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:         "acme-claude",
		apiType:         "anthropic",
		modelName:       "claude-sonnet-4",
		reviewMode:      "mutable",
		fsMode:          "auto",
		keyStorage:      "env",
		envVarName:      "ACME_CLAUDE_API_KEY",
		baseURLInput:    "https://acme.example.com/v1",
		addedModelNames: []string{"claude-sonnet-4"},
		addedModel:      true,
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
		reviewMode:      "mutable",
		fsMode:          "auto",
		keyStorage:      "env",
		envVarName:      "GROQ_API_KEY",
		addedModelNames: []string{"llama"},
		addedModel:      true,
	})
	requireNoUpdatePath(t, updates, []string{"apis", "groq", "api-type"})
}

func TestBuildConfigWizardUpdatesExistingProviderDoesNotRewriteBaseURL(t *testing.T) {
	updates := buildConfigWizardUpdates(configWizardSaveData{
		apiName:                "openrouter",
		modelName:              "vendor/gpt-5.5:latest",
		reviewMode:             "mutable",
		fsMode:                 "auto",
		webSearchProvider:      "duckduckgo",
		webSearchProviderValue: "duckduckgo",
		keyStorage:             "env",
		envVarName:             "OPENROUTER_API_KEY",
		addedModelNames:        []string{"vendor/gpt-5.5:latest"},
		addedModel:             true,
	})

	requireNoUpdatePath(t, updates, []string{"apis", "openrouter", "base-url"})
	requireNoUpdatePath(t, updates, []string{"apis", "openrouter", "api-type"})
	requireUpdateValue(t, updates, []string{"apis", "openrouter", "models", "vendor/gpt-5.5:latest", "max-input-chars"}, defaultNewModelInputChars)
}

func TestPrintConfigSummaryShowsEffectiveBaseURL(t *testing.T) {
	output := captureStderr(t, func() {
		printConfigSummary(summaryData{
			api:                 "openrouter",
			model:               "vendor/gpt-5.5:latest",
			keyStorage:          "env",
			envVarName:          "OPENROUTER_API_KEY",
			baseURL:             "https://openrouter.ai/api/v1",
			addedModelCount:     2,
			fsMode:              "auto",
			webSearchProvider:   "duckduckgo",
			webSearchKeyStorage: "env",
			reviewMode:          "mutable",
			settingsPath:        "/tmp/mods.yml",
		})
	})

	require.Contains(t, output, "Base URL")
	require.Contains(t, output, "https://openrouter.ai/api/v1")
	require.Contains(t, output, "Added models")
	require.Contains(t, output, "default, first line")
	// Default (OpenAI-compatible) providers must not show an API type row.
	require.NotContains(t, output, "API type")
}

func TestPrintConfigSummaryShowsAPITypeForAnthropic(t *testing.T) {
	output := captureStderr(t, func() {
		printConfigSummary(summaryData{
			api:          "acme-claude",
			model:        "claude-sonnet-4",
			apiType:      "anthropic",
			keyStorage:   "env",
			envVarName:   "ACME_CLAUDE_API_KEY",
			baseURL:      "https://acme.example.com/v1",
			fsMode:       "auto",
			reviewMode:   "mutable",
			settingsPath: "/tmp/mods.yml",
		})
	})
	require.Contains(t, output, "API type")
	require.Contains(t, output, "anthropic")
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

func TestParseNewModelNamesTrimsSkipsEmptyAndDeduplicates(t *testing.T) {
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
		models, err := parseNewModelNames("openrouter", "\n vendor/gpt-5.5:latest \n\nvendor/gpt-5.5:latest\nopenai/gpt-5.4\n")
		require.NoError(t, err)
		require.Equal(t, []string{"vendor/gpt-5.5:latest", "openai/gpt-5.4"}, models)

		_, err = parseNewModelNames("openrouter", "\n \t")
		require.Error(t, err)
		_, err = parseNewModelNames("openrouter", "anthropic/claude-sonnet-4-6")
		require.Error(t, err)
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

// TestPromptDiscoveredModelsAllConfigured locks in the fix for the
// "Configuration wizard failed" bug: when every discovered model is already on
// the provider, the picker returns (nil, nil) WITHOUT erroring or showing a
// form, so the wizard can fall back to manual entry.
func TestPromptDiscoveredModelsAllConfigured(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{
		APIs: []API{{
			Name:   "deepseek",
			Models: map[string]Model{"deepseek-chat": {}, "deepseek-reasoner": {}},
		}},
	}}, func() {
		picked, err := promptDiscoveredModels("deepseek", []string{"deepseek-chat", "deepseek-reasoner"})
		require.NoError(t, err)
		require.Nil(t, picked)
	})
}
