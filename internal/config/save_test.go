package config

import (
	"os"
	"path/filepath"
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
  sequential-thinking: false
`)

	require.NoError(t, SaveFields(path, map[string]any{
		"builtin-tools.filesystem":          "true",
		"builtin-tools.shell":               true,
		"builtin-tools.sequential-thinking": true,
	}))

	m := loadAsMap(t, path)
	bt := m["builtin-tools"].(map[string]any)
	require.Equal(t, "true", bt["filesystem"])
	require.Equal(t, true, bt["shell"])
	require.Equal(t, true, bt["sequential-thinking"])
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

func TestApplyEnvCustomProvider(t *testing.T) {
	t.Setenv("MODS_BASE_URL", "")
	t.Setenv("MODS_API_KEY", "")

	t.Run("noop when env vars not set", func(t *testing.T) {
		c := &Config{}
		c.API = "openai"
		applyEnvCustomProvider(c)
		require.NotContains(t, providerNames(c.APIs), "custom")
		require.Equal(t, "openai", c.API)
	})

	t.Run("adds custom provider and switches default when no key", func(t *testing.T) {
		t.Setenv("MODS_BASE_URL", "https://my-proxy.example.com/v1")
		t.Setenv("MODS_API_KEY", "sk-proxy-123")

		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKeyEnv: "DEFINITELY_NOT_SET"}}
		applyEnvCustomProvider(c)

		require.Contains(t, providerNames(c.APIs), "custom")
		require.Equal(t, "custom", c.API, "default should switch to custom when no key resolvable")
	})

	t.Run("adds custom provider but keeps default when key exists", func(t *testing.T) {
		t.Setenv("MODS_BASE_URL", "https://my-proxy.example.com/v1")
		t.Setenv("MODS_API_KEY", "sk-proxy-123")

		c := &Config{}
		c.API = "openai"
		c.APIs = []API{{Name: "openai", APIKey: "sk-existing"}}
		applyEnvCustomProvider(c)

		require.Contains(t, providerNames(c.APIs), "custom")
		require.Equal(t, "openai", c.API, "default should NOT switch when existing key works")
	})

	t.Run("updates existing custom provider in place", func(t *testing.T) {
		t.Setenv("MODS_BASE_URL", "https://new-proxy.example.com/v1")
		t.Setenv("MODS_API_KEY", "sk-new-456")

		c := &Config{}
		c.API = "custom"
		c.APIs = []API{{
			Name:    "custom",
			BaseURL: "https://old-proxy.example.com/v1",
			APIKey:  "sk-old",
		}}
		applyEnvCustomProvider(c)

		require.Len(t, c.APIs, 1, "should not duplicate custom provider")
		require.Equal(t, "https://new-proxy.example.com/v1", c.APIs[0].BaseURL)
		require.Equal(t, "sk-new-456", c.APIs[0].APIKey)
	})

	t.Run("only one env var set is noop", func(t *testing.T) {
		t.Setenv("MODS_BASE_URL", "https://my-proxy.example.com/v1")
		t.Setenv("MODS_API_KEY", "")

		c := &Config{}
		c.API = "openai"
		applyEnvCustomProvider(c)
		require.NotContains(t, providerNames(c.APIs), "custom")
	})
}

func providerNames(apis []API) []string {
	names := make([]string, len(apis))
	for i, a := range apis {
		names[i] = a.Name
	}
	return names
}
