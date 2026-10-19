package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/stretchr/testify/require"
)

type goDiscoveryTransport func(*http.Request) (*http.Response, error)

func (f goDiscoveryTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestOpenCodeGoDiscoveryUsesUnifiedCatalogAndFiltersProtocols(t *testing.T) {
	client := &http.Client{Transport: goDiscoveryTransport(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, providerinfo.OpenCodeGoModelsURL, r.URL.String())
		require.Equal(t, "Bearer key", r.Header.Get("Authorization"))
		require.Equal(t, "setup-id", r.Header.Get("x-opencode-session"))
		require.Equal(t, "mods/1.0", r.UserAgent())
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"data":[{"id":"gpt-6-luna"},{"id":"deepseek-v4-pro"},{"id":"minimax-m3"},{"id":"qwen3.8-max"},{"id":"gemini-4-argon-contributor"},{"id":"future-model"}]}`))}, nil
	})}
	base := providerinfo.DefaultBaseURL("opencode-go")
	models, endpoints, unknown, err := discoverOpenCodeGoModelsWithClientContext(context.Background(), client, "openai", base, "key", "setup-id")
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-pro", "gpt-6-luna"}, models)
	require.Equal(t, map[string]string{"deepseek-v4-pro": "chat-completions", "gpt-6-luna": "responses"}, endpoints)
	require.Equal(t, []string{"future-model"}, unknown)
	models, endpoints, unknown, err = discoverOpenCodeGoModelsWithClientContext(context.Background(), client, "anthropic", providerinfo.DefaultBaseURL("opencode-go-anthropic"), "key", "setup-id")
	require.NoError(t, err)
	require.Equal(t, []string{"minimax-m3", "qwen3.8-max"}, models)
	require.Empty(t, endpoints, "Messages routing belongs to the provider, not a model endpoint override")
	require.Equal(t, []string{"future-model"}, unknown)
}

func TestOpenCodeGoSavePreservesCuratedModelsAndExplicitEndpoints(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{APIs: []API{{Name: "alias", Models: map[string]Model{
		"gpt-6-luna":   {Aliases: []string{"luna"}},
		"gpt-5.6-luna": {Endpoint: "chat-completions"},
	}}}}}, func() {
		updates := buildConfigWizardUpdates(configWizardSaveData{
			apiName: "alias", baseURLInput: providerinfo.DefaultBaseURL("opencode-go"),
			addedModelNames: []string{"gpt-6-luna", "gpt-5.6-luna", "grok-4.7"},
			modelEndpoints:  map[string]string{"gpt-6-luna": "responses", "gpt-5.6-luna": "responses", "grok-4.7": "responses"},
		})
		requireUpdateValue(t, updates, []string{"apis", "alias", "models", "gpt-6-luna", "endpoint"}, "responses")
		requireNoUpdatePath(t, updates, []string{"apis", "alias", "models", "gpt-6-luna"})
		requireNoUpdatePath(t, updates, []string{"apis", "alias", "models", "gpt-5.6-luna", "endpoint"})
		requireUpdateValue(t, updates, []string{"apis", "alias", "models", "grok-4.7"}, map[string]any{"endpoint": "responses"})
		path := writeCLIConfig(t, "apis:\n  alias:\n    models:\n      gpt-6-luna:\n        aliases: [luna]\n        extra-params: {temperature: 0.4}\n      gpt-5.6-luna:\n        endpoint: chat-completions\n")
		require.NoError(t, SaveFieldPaths(path, updates))
		saved := loadCLIConfig(t, path)["apis"].(map[string]any)["alias"].(map[string]any)["models"].(map[string]any)
		luna := saved["gpt-6-luna"].(map[string]any)
		require.Equal(t, "responses", luna["endpoint"])
		require.NotEmpty(t, luna["aliases"])
		require.NotEmpty(t, luna["extra-params"])
		require.Equal(t, "chat-completions", saved["gpt-5.6-luna"].(map[string]any)["endpoint"])
	})
}

func TestSetupOpenCodeGoManualRoutingAndUnknownModels(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		m.provider = "opencode-go"
		m.draft().manual, m.draft().defaultModel = "gpt-6-luna", "gpt-6-luna"
		data, err := m.saveData()
		require.NoError(t, err)
		require.Equal(t, "responses", data.modelEndpoints["gpt-6-luna"])
		config.APIs = append(config.APIs, API{Name: "opencode-go", Models: map[string]Model{"gpt-6-luna": {Endpoint: "chat-completions"}}})
		data, err = m.saveData()
		require.NoError(t, err)
		require.Equal(t, "chat-completions", data.modelEndpoints["gpt-6-luna"], "explicit route must win over discovery")
		m.draft().manual, m.draft().defaultModel = "future-model", "future-model"
		_, err = m.saveData()
		require.ErrorContains(t, err, "unconfirmed")
		m.draft().manual, m.draft().defaultModel = "qwen3.8-max", "qwen3.8-max"
		_, err = m.saveData()
		require.ErrorContains(t, err, "anthropic")
		m.draft().unconfirmed = []string{"future-model"}
		m.page = setupDiscovery
		_, description := m.heading()
		require.Contains(t, description, "Unconfirmed protocol")
		m.invalidate()
		require.Empty(t, m.draft().unconfirmed)
	})
}

func TestSetupConnectionUsesPersistedEndpointAndEnvironmentCredential(t *testing.T) {
	setupFixture(t, func(m *setupModel) {
		t.Setenv("MODS_TEST_CONNECTION_KEY", "test-credential")
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/responses", r.URL.Path)
			require.Equal(t, "Bearer test-credential", r.Header.Get("Authorization"))
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			require.NotEmpty(t, body["input"])
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"r\",\"status\":\"completed\",\"output\":[]}}\n\n")
		}))
		defer server.Close()
		config.APIs = append(config.APIs, API{Name: "gateway", APIKeyEnv: "MODS_TEST_CONNECTION_KEY", BaseURL: server.URL, Models: map[string]Model{"model": {Endpoint: "responses"}}})
		m.provider = "gateway"
		m.draft().manual, m.draft().defaultModel = "model", "model"
		var saved configWizardSaveData
		m.save = func(_, _ string, data configWizardSaveData) error { saved = data; return nil }
		cmd := m.checkConnection()
		require.NotNil(t, cmd)
		m.Update(cmd())
		require.NoError(t, m.err)
		require.True(t, m.done)
		require.Equal(t, "responses", saved.modelEndpoints["model"])
	})
}

func TestConnectionErrorsAreBoundedAndRedacted(t *testing.T) {
	for _, code := range []int{401, 403} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(code)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"message": "test-secret access denied " + strings.Repeat("x", 1000), "type": "access_error"}})
		}))
		err := testConnectionContext(context.Background(), "model", openai.Config{BaseURL: server.URL, HTTPClient: server.Client(), AuthToken: "test-secret", UseResponses: true})
		server.Close()
		require.Error(t, err)
		require.Contains(t, err.Error(), fmt.Sprint(code))
		require.NotContains(t, err.Error(), "test-secret")
		require.NotContains(t, err.Error(), "invalid API key")
		require.Less(t, len([]rune(err.Error())), 550)
	}
}
