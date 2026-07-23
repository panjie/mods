package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// writeTestConfig writes the given YAML to a temp file and returns its path.
func writeTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "mods.yml")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

// loadAsMap reads the YAML file back into a generic map for assertions.
func loadAsMap(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, yaml.Unmarshal(data, &m))
	return m
}

func TestSaveFields_TopLevel(t *testing.T) {
	path := writeTestConfig(t, `# my config
default-api: openai
default-model: gpt-5.4
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"default-api":   "deepseek",
		"default-model": "deepseek-v4-flash",
	}))

	m := loadAsMap(t, path)
	require.Equal(t, "deepseek", m["default-api"])
	require.Equal(t, "deepseek-v4-flash", m["default-model"])
}

func TestSaveFields_PreservesComments(t *testing.T) {
	path := writeTestConfig(t, `# top comment
default-api: openai  # inline comment
# section comment
default-model: gpt-5.4
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"default-api": "anthropic",
	}))

	// Comments must survive the round-trip.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	s := string(data)
	require.Contains(t, s, "# top comment")
	require.Contains(t, s, "# inline comment")
	require.Contains(t, s, "# section comment")
}

func TestSaveFields_NestedMapping(t *testing.T) {
	path := writeTestConfig(t, `builtin-tools:
  filesystem: auto
  shell: false
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"builtin-tools.filesystem": "true",
		"builtin-tools.shell":      true,
	}))

	m := loadAsMap(t, path)
	bt := m["builtin-tools"].(map[string]any)
	require.Equal(t, "true", bt["filesystem"])
	require.Equal(t, true, bt["shell"])
}

func TestSaveFields_DeeplyNested(t *testing.T) {
	path := writeTestConfig(t, `apis:
  openai:
    base-url: https://api.openai.com/v1
    api-key-env: OPENAI_API_KEY
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"apis.openai.api-key-env": "MY_CUSTOM_KEY",
		"apis.openai.api-key":     "sk-test-123",
	}))

	m := loadAsMap(t, path)
	apis := m["apis"].(map[string]any)
	openai := apis["openai"].(map[string]any)
	require.Equal(t, "MY_CUSTOM_KEY", openai["api-key-env"])
	require.Equal(t, "sk-test-123", openai["api-key"])
	// Untouched field must survive.
	require.Equal(t, "https://api.openai.com/v1", openai["base-url"])
}

func TestSaveFields_CreatesMissingPath(t *testing.T) {
	path := writeTestConfig(t, `default-api: openai
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"builtin-tools.filesystem": "auto",
		"builtin-tools.shell":      false,
	}))

	m := loadAsMap(t, path)
	bt := m["builtin-tools"].(map[string]any)
	require.Equal(t, "auto", bt["filesystem"])
	require.Equal(t, false, bt["shell"])
}

func TestSaveFields_CreatesMissingDeepPath(t *testing.T) {
	path := writeTestConfig(t, `default-api: openai
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"apis.groq.base-url":    "https://api.groq.com/openai/v1",
		"apis.groq.api-key-env": "GROQ_API_KEY",
	}))

	m := loadAsMap(t, path)
	apis := m["apis"].(map[string]any)
	groq := apis["groq"].(map[string]any)
	require.Equal(t, "https://api.groq.com/openai/v1", groq["base-url"])
	require.Equal(t, "GROQ_API_KEY", groq["api-key-env"])
}

func TestSaveFieldPaths_ModelNameWithSeparators(t *testing.T) {
	path := writeTestConfig(t, `apis:
  openrouter:
    models: {}
`)

	require.NoError(t, SaveFieldPaths(path, []FieldUpdate{
		{
			Path:  []string{"apis", "openrouter", "models", "vendor/gpt-5.5:latest", "max-input-chars"},
			Value: 1000000,
		},
	}))

	m := loadAsMap(t, path)
	apis := m["apis"].(map[string]any)
	openrouter := apis["openrouter"].(map[string]any)
	models := openrouter["models"].(map[string]any)
	model := models["vendor/gpt-5.5:latest"].(map[string]any)
	require.Equal(t, 1000000, model["max-input-chars"])
	require.NotContains(t, models, "vendor/gpt-5")
}

func TestSaveFields_BoolSerialization(t *testing.T) {
	path := writeTestConfig(t, `builtin-tools:
  shell: false
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"builtin-tools.shell": true,
	}))

	// The YAML should contain an unquoted bool, not a string.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "shell: true")
	require.NotContains(t, string(data), `"true"`)
}

func TestSaveFieldPaths_EmptyMapCreatesEmptyMapping(t *testing.T) {
	path := writeTestConfig(t, `default-api: openai
apis: {}
`)

	require.NoError(t, SaveFieldPaths(path, []FieldUpdate{
		{Path: []string{"apis", "groq", "models", "llama-3.3-70b-versatile"}, Value: map[string]any{}},
	}))

	// Round-trip: the model entry exists as an empty mapping.
	m := loadAsMap(t, path)
	apis := m["apis"].(map[string]any)
	groq := apis["groq"].(map[string]any)
	models := groq["models"].(map[string]any)
	model := models["llama-3.3-70b-versatile"].(map[string]any)
	require.Empty(t, model)

	// On disk the model renders as `name: {}` so the file stays readable
	// and the entry is unmistakably empty rather than missing.
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "llama-3.3-70b-versatile: {}")
}

func TestSaveFieldPaths_NonEmptyMapWritesMapping(t *testing.T) {
	path := writeTestConfig(t, `default-api: openai
apis: {}
`)

	require.NoError(t, SaveFieldPaths(path, []FieldUpdate{
		{Path: []string{"apis", "groq", "models", "llama-3.3-70b-versatile"}, Value: map[string]any{
			"max-input-chars": 800000,
			"fallback":        "llama-3.1-8b-instant",
		}},
	}))

	m := loadAsMap(t, path)
	apis := m["apis"].(map[string]any)
	groq := apis["groq"].(map[string]any)
	models := groq["models"].(map[string]any)
	model := models["llama-3.3-70b-versatile"].(map[string]any)
	require.Equal(t, 800000, model["max-input-chars"])
	require.Equal(t, "llama-3.1-8b-instant", model["fallback"])
}

func TestSaveFields_DeleteField(t *testing.T) {
	path := writeTestConfig(t, `web-search: true
web-search-provider: tavily
web-search-api-key: tvly-test
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"web-search-api-key": nil,
	}))

	m := loadAsMap(t, path)
	require.Equal(t, true, m["web-search"])
	require.Equal(t, "tavily", m["web-search-provider"])
	require.NotContains(t, m, "web-search-api-key")
}

func TestMergeSettingsYAMLRecursivelyMergesMappings(t *testing.T) {
	path := writeTestConfig(t, `# user config
default-api: openai
apis:
  fujitsu-google:
    base-url: https://old.example.com  # keep this comment
    api-key-env: FUJITSU_GOOGLE_API_KEY
    models:
      existing-model: {}
skills-dirs:
  - ./existing-skills
`)

	patch := []byte(`default-api: fujitsu-google
apis:
  fujitsu-google:
    api-type: google
    base-url: https://abc.com
    api-key: sk-abcde
    models:
      gemini-3.0-pro: {}
`)
	require.NoError(t, MergeSettingsYAML(path, patch))

	m := loadAsMap(t, path)
	require.Equal(t, "fujitsu-google", m["default-api"])
	apis := m["apis"].(map[string]any)
	provider := apis["fujitsu-google"].(map[string]any)
	require.Equal(t, "google", provider["api-type"])
	require.Equal(t, "https://abc.com", provider["base-url"])
	require.Equal(t, "sk-abcde", provider["api-key"])
	require.Equal(t, "FUJITSU_GOOGLE_API_KEY", provider["api-key-env"])
	models := provider["models"].(map[string]any)
	require.Contains(t, models, "existing-model")
	require.Contains(t, models, "gemini-3.0-pro")
	require.Equal(t, []any{"./existing-skills"}, m["skills-dirs"])

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "# user config")
	require.Contains(t, string(data), "# keep this comment")
	require.Less(t,
		strings.Index(string(data), "existing-model"),
		strings.Index(string(data), "gemini-3.0-pro"),
	)
}

func TestMergeSettingsYAMLReplacesSequencesScalarsAndNodeKinds(t *testing.T) {
	path := writeTestConfig(t, `skills-dirs:
  - ./old
custom-value:
  nested: old
default-model: old-model
`)

	require.NoError(t, MergeSettingsYAML(path, []byte(`skills-dirs:
  - ./one
  - ./two
custom-value:
  - replacement
default-model: null
`)))

	m := loadAsMap(t, path)
	require.Equal(t, []any{"./one", "./two"}, m["skills-dirs"])
	require.Equal(t, []any{"replacement"}, m["custom-value"])
	require.Contains(t, m, "default-model")
	require.Nil(t, m["default-model"])
}

func TestMergeSettingsYAMLBootstrapsDefaultFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, WriteDefaultFile(path))

	require.NoError(t, MergeSettingsYAML(path, []byte("default-api: ollama\n")))

	m := loadAsMap(t, path)
	require.Equal(t, "ollama", m["default-api"])
	require.Contains(t, m, "format-text")
}

func TestMergeSettingsYAMLRejectsInvalidPatchWithoutChangingFile(t *testing.T) {
	tests := map[string]string{
		"empty":                "",
		"empty mapping":        "{}",
		"sequence root":        "- default-api\n- openai\n",
		"scalar root":          "default-api",
		"multiple documents":   "default-api: openai\n---\ndefault-api: ollama\n",
		"invalid YAML":         "apis: [",
		"invalid config shape": "apis:\n  custom:\n    models: model-name\n",
	}

	for name, patch := range tests {
		t.Run(name, func(t *testing.T) {
			original := []byte("# untouched\ndefault-api: openai\n")
			path := writeTestConfig(t, string(original))

			require.Error(t, MergeSettingsYAML(path, []byte(patch)))

			after, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, original, after)
		})
	}
}

func TestMergeSettingsYAMLRejectsInvalidExistingConfig(t *testing.T) {
	original := []byte("apis: [")
	path := writeTestConfig(t, string(original))

	require.Error(t, MergeSettingsYAML(path, []byte("default-api: openai\n")))

	after, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, original, after)
}

func TestHasAPIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	t.Run("ollama is always OK", func(t *testing.T) {
		c := &Config{}
		c.API = "ollama"
		require.True(t, HasAPIKey(c))
	})

	t.Run("direct api-key field", func(t *testing.T) {
		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKey: "sk-test"}}
		require.True(t, HasAPIKey(c))
	})

	t.Run("env var set", func(t *testing.T) {
		t.Setenv("MY_OPENAI_KEY", "sk-from-env")
		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKeyEnv: "MY_OPENAI_KEY"}}
		require.True(t, HasAPIKey(c))
	})

	t.Run("env var configured but not set", func(t *testing.T) {
		t.Setenv("MISSING_KEY", "")
		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKeyEnv: "MISSING_KEY"}}
		require.False(t, HasAPIKey(c))
	})

	t.Run("no key at all", func(t *testing.T) {
		c := &Config{}
		c.API = "deepseek"
		c.APIs = []API{{Name: "deepseek", APIKeyEnv: "DEFINITELY_NOT_SET_KEY_XYZ"}}
		require.False(t, HasAPIKey(c))
	})

	t.Run("api-key-cmd", func(t *testing.T) {
		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKeyCmd: "rbw get openai"}}
		require.True(t, HasAPIKey(c))
	})
}

func TestThemeHelpCoversInteractivePanels(t *testing.T) {
	require.Contains(t, Help["theme"], "forms and panels")
}
