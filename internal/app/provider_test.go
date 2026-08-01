package app

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestSanitizeProviderContinuationFiltersIncompatibleOpaqueState(t *testing.T) {
	m := &Mods{
		Config: &Config{},
		messages: []proto.Message{{
			Role: proto.RoleAssistant,
			ProviderData: map[string]json.RawMessage{
				"openai.responses.output.v1":   json.RawMessage(`[]`),
				"deepseek.responses.output.v1": json.RawMessage(`[]`),
				"deepseek.chat.assistant.v1":   json.RawMessage(`{}`),
			},
		}},
	}
	m.sanitizeProviderContinuation(Model{API: "deepseek", ProviderProfile: "deepseek"}, true)
	require.Equal(t, map[string]json.RawMessage{
		"deepseek.responses.output.v1": json.RawMessage(`[]`),
	}, m.messages[0].ProviderData)
}

// TestApplyGoogleBaseURLOverride pins the {model} template semantics for the
// user-supplied Gemini endpoint. The placeholder must be path-escaped, the
// URL must be used verbatim when no placeholder is present, and substitution
// must not touch anything else in the URL.
func TestApplyGoogleBaseURLOverride(t *testing.T) {
	t.Run("substitutes {model} placeholder", func(t *testing.T) {
		base := "https://my-proxy.example.com/v1beta/models/{model}:streamGenerateContent?alt=sse"
		got := applyGoogleBaseURLOverride(base, "gemini-2.5-pro")
		require.Equal(t,
			"https://my-proxy.example.com/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
			got,
		)
	})

	t.Run("escapes path-unsafe characters in the model name", func(t *testing.T) {
		base := "https://my-proxy.example.com/v1beta/models/{model}:streamGenerateContent?alt=sse"
		got := applyGoogleBaseURLOverride(base, "models/gemini-2.5-pro")
		require.Contains(t, got, "models%2Fgemini-2.5-pro")
		require.Equal(t, url.PathEscape("models/gemini-2.5-pro"),
			"models%2Fgemini-2.5-pro", "test premise: PathEscape encodes the slash")
	})

	t.Run("uses URL verbatim when there is no placeholder", func(t *testing.T) {
		base := "https://single-model-proxy.example.com/some/path:streamGenerateContent?alt=sse"
		got := applyGoogleBaseURLOverride(base, "ignored")
		require.Equal(t, base, got)
	})

	t.Run("replaces every occurrence of {model}", func(t *testing.T) {
		base := "https://h.example.com/api/{model}/stream?label={model}"
		got := applyGoogleBaseURLOverride(base, "gem")
		require.Equal(t, "https://h.example.com/api/gem/stream?label=gem", got)
	})
}

func TestUseOfficialOpenAIResponses(t *testing.T) {
	tests := []struct {
		name    string
		api     string
		baseURL string
		want    bool
	}{
		{name: "empty official URL", api: "openai", want: true},
		{name: "official URL", api: "openai", baseURL: "https://api.openai.com/v1", want: true},
		{name: "official URL case insensitive", api: "openai", baseURL: "https://API.OPENAI.COM/v1/", want: true},
		{name: "custom URL under openai profile", api: "openai", baseURL: "https://proxy.example.com/v1"},
		{name: "custom provider", api: "groq", baseURL: "https://api.openai.com/v1"},
		{name: "Azure", api: "azure", baseURL: "https://example.openai.azure.com"},
		{name: "malformed URL", api: "openai", baseURL: "://bad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, useOfficialOpenAIResponses(tt.api, tt.baseURL))
		})
	}

	t.Run("empty official URL resolves before SDK construction", func(t *testing.T) {
		mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
		cfgs, err := mods.buildProviderConfigs(
			Model{Name: "deepseek-v4-flash", API: "deepseek"},
			API{Name: "deepseek", APIKey: "test-key"},
		)
		require.NoError(t, err)
		require.Equal(t, "https://api.deepseek.com", cfgs.OpenAI.BaseURL)
		require.True(t, cfgs.OpenAI.UseResponses)
	})
}

func TestProviderCapabilitiesOwnJSONResponseFormatSupport(t *testing.T) {
	tests := []struct {
		api  string
		want bool
	}{
		{api: "openai", want: true},
		{api: "custom-openai-compatible", want: true},
		{api: "anthropic"},
		{api: "google"},
		{api: "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.api, func(t *testing.T) {
			client, err := newStreamClient(
				tt.api,
				anthropic.Config{},
				google.Config{},
				ollama.Config{},
				openai.Config{},
			)
			require.NoError(t, err)
			require.Equal(t, tt.want, client.Capabilities().JSONResponseFormat)
		})
	}
}

func TestBuildProviderConfigsSelectsResponsesOnlyForOfficialOpenAI(t *testing.T) {
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}

	official, err := mods.buildProviderConfigs(
		Model{Name: "gpt-5.4-mini", API: "openai"},
		API{Name: "openai", APIKey: "test-key", BaseURL: "https://api.openai.com/v1"},
	)
	require.NoError(t, err)
	require.True(t, official.OpenAI.UseResponses)

	custom, err := mods.buildProviderConfigs(
		Model{Name: "gpt-5.4-mini", API: "openai"},
		API{Name: "openai", APIKey: "test-key", BaseURL: "https://proxy.example.com/v1"},
	)
	require.NoError(t, err)
	require.False(t, custom.OpenAI.UseResponses)

	azure, err := mods.buildProviderConfigs(
		Model{Name: "deployment", API: "azure"},
		API{Name: "azure", APIKey: "test-key", BaseURL: "https://example.openai.azure.com"},
	)
	require.NoError(t, err)
	require.False(t, azure.OpenAI.UseResponses)
}

func TestBuildProviderConfigsDeepSeekResponsesRouting(t *testing.T) {
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	tests := []struct {
		name      string
		model     Model
		baseURL   string
		responses bool
		profile   openai.ResponsesProfile
	}{
		{name: "official flash", model: Model{Name: "deepseek-v4-flash", API: "deepseek"}, baseURL: "https://api.deepseek.com/", responses: true, profile: openai.ResponsesProfileDeepSeek},
		{name: "official pro stays chat", model: Model{Name: "deepseek-v4-pro", API: "deepseek"}, baseURL: "https://api.deepseek.com/", profile: openai.ResponsesProfileDeepSeek},
		{name: "explicit chat wins", model: Model{Name: "deepseek-v4-flash", API: "deepseek", Endpoint: "chat-completions"}, baseURL: "https://api.deepseek.com/", profile: openai.ResponsesProfileDeepSeek},
		{name: "custom gateway defaults chat", model: Model{Name: "deepseek-v4-flash", API: "deepseek"}, baseURL: "https://proxy.example.com/v1", profile: openai.ResponsesProfileDeepSeek},
		{name: "custom gateway explicit responses", model: Model{Name: "deepseek-v4-flash", API: "deepseek", Endpoint: "responses"}, baseURL: "https://proxy.example.com/v1", responses: true, profile: openai.ResponsesProfileDeepSeek},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfgs, err := mods.buildProviderConfigs(tt.model, API{Name: "deepseek", APIKey: "test-key", BaseURL: tt.baseURL})
			require.NoError(t, err)
			require.Equal(t, tt.responses, cfgs.OpenAI.UseResponses)
			require.Equal(t, tt.profile, cfgs.OpenAI.ResponsesProfile)
		})
	}
}

func TestBuildProviderConfigsCustomDeepSeekProfile(t *testing.T) {
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	cfgs, err := mods.buildProviderConfigs(
		Model{
			Name: "deepseek-v4-flash", API: "company-router", Protocol: "openai",
			ProviderProfile: "deepseek", Endpoint: "responses",
		},
		API{Name: "company-router", APIKey: "test-key", BaseURL: "https://gateway.example.com/v1"},
	)
	require.NoError(t, err)
	require.True(t, cfgs.OpenAI.UseResponses)
	require.Equal(t, openai.ProviderProfileDeepSeek, cfgs.OpenAI.ProviderProfile)
}

func TestBuildProviderConfigsClonesExtraParams(t *testing.T) {
	extra := map[string]any{"thinking": map[string]any{"type": "disabled"}, "items": []any{map[string]any{"x": true}}}
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	cfgs, err := mods.buildProviderConfigs(
		Model{Name: "deepseek-v4-flash", API: "deepseek", ExtraParams: extra},
		API{Name: "deepseek", APIKey: "test-key"},
	)
	require.NoError(t, err)
	cfgs.OpenAI.ExtraParams["thinking"].(map[string]any)["type"] = "enabled"
	cfgs.OpenAI.ExtraParams["items"].([]any)[0].(map[string]any)["x"] = false
	require.Equal(t, "disabled", extra["thinking"].(map[string]any)["type"])
	require.Equal(t, true, extra["items"].([]any)[0].(map[string]any)["x"])
}

func TestBuildProviderConfigsCustomProviderRequiresBaseURL(t *testing.T) {
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	_, err := mods.buildProviderConfigs(
		Model{Name: "model", API: "custom", Protocol: "openai"},
		API{Name: "custom", APIKey: "test-key"},
	)
	require.ErrorContains(t, err, "requires base-url")
}

func TestBuildProviderConfigsRejectsInvalidEndpoint(t *testing.T) {
	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	_, err := mods.buildProviderConfigs(
		Model{Name: "deepseek-v4-flash", API: "deepseek", Endpoint: "messages"},
		API{Name: "deepseek", APIKey: "test-key", BaseURL: "https://api.deepseek.com/"},
	)
	require.ErrorContains(t, err, "expected responses or chat-completions")
}

func TestBuildProviderConfigsGitHubCopilotUsesOpenAICompatibleChat(t *testing.T) {
	oldExchange := exchangeCopilotToken
	defer func() { exchangeCopilotToken = oldExchange }()
	exchangeCopilotToken = func(_ string) (string, error) { return "copilot-api-token", nil }

	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	cfgs, err := mods.buildProviderConfigs(
		Model{Name: "gemini-2.5-pro", API: "github-copilot", Endpoint: "chat-completions"},
		API{Name: "github-copilot", APIKey: "github-oauth-token", BaseURL: "https://api.githubcopilot.com"},
	)
	require.NoError(t, err)
	require.Equal(t, "copilot-api-token", cfgs.OpenAI.AuthToken)
	require.Equal(t, "https://api.githubcopilot.com", cfgs.OpenAI.BaseURL)
	require.False(t, cfgs.OpenAI.UseResponses)
	require.Equal(t, "mods/1.0", cfgs.OpenAI.Headers["Editor-Version"])
}

func TestBuildProviderConfigsGitHubCopilotUsesResponsesEndpoint(t *testing.T) {
	oldExchange := exchangeCopilotToken
	defer func() { exchangeCopilotToken = oldExchange }()
	exchangeCopilotToken = func(_ string) (string, error) { return "copilot-api-token", nil }

	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	cfgs, err := mods.buildProviderConfigs(
		Model{Name: "gpt-5.4-mini", API: "github-copilot", Endpoint: "responses"},
		API{Name: "github-copilot", APIKey: "github-oauth-token", BaseURL: "https://api.githubcopilot.com"},
	)
	require.NoError(t, err)
	require.True(t, cfgs.OpenAI.UseResponses)
}

func TestBuildProviderConfigsGitHubCopilotFallbacksGpt5ToResponses(t *testing.T) {
	oldExchange := exchangeCopilotToken
	defer func() { exchangeCopilotToken = oldExchange }()
	exchangeCopilotToken = func(_ string) (string, error) { return "copilot-api-token", nil }

	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	cfgs, err := mods.buildProviderConfigs(
		Model{Name: "gpt-5.4-mini", API: "github-copilot"},
		API{Name: "github-copilot", APIKey: "github-oauth-token", BaseURL: "https://api.githubcopilot.com"},
	)
	require.NoError(t, err)
	require.True(t, cfgs.OpenAI.UseResponses)
}

func TestBuildProviderConfigsGitHubCopilotMessagesEndpointUnsupported(t *testing.T) {
	oldExchange := exchangeCopilotToken
	defer func() { exchangeCopilotToken = oldExchange }()
	exchangeCopilotToken = func(_ string) (string, error) { return "copilot-api-token", nil }

	mods := &Mods{Styles: makeStyles(true), Config: &Config{}}
	_, err := mods.buildProviderConfigs(
		Model{Name: "claude-sonnet-4", API: "github-copilot", Endpoint: "messages"},
		API{Name: "github-copilot", APIKey: "github-oauth-token", BaseURL: "https://api.githubcopilot.com"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "requires /v1/messages")
}

// TestBuildProviderConfigsGoogleUsesUserBaseURL closes the regression that
// originally motivated the fix: an api.BaseURL entry in mods.yml was
// silently ignored for Google, so users targeting a Vertex proxy or a
// reverse-proxy stayed pinned to generativelanguage.googleapis.com.
func TestBuildProviderConfigsGoogleUsesUserBaseURL(t *testing.T) {
	customBase := "https://vertex-proxy.example.com/v1beta/models/{model}:streamGenerateContent?alt=sse"
	mods := &Mods{
		Styles: makeStyles(true),
		Config: &Config{
			PersistentConfig: PersistentConfig{
				APIs: []API{{
					Name:    "google",
					APIKey:  "test-key",
					BaseURL: customBase,
					Models: map[string]Model{
						"gemini-2.5-flash": {
							Name: "gemini-2.5-flash",
							API:  "google",
						},
					},
				}},
			},
		},
	}
	api := mods.Config.APIs[0]
	cfgs, err := mods.buildProviderConfigs(Model{Name: "gemini-2.5-flash", API: "google"}, api)
	require.NoError(t, err)
	require.Equal(t,
		"https://vertex-proxy.example.com/v1beta/models/gemini-2.5-flash:streamGenerateContent?alt=sse",
		cfgs.Google.BaseURL,
	)
}

// TestApplyHTTPProxyAlsoConfiguresGoogle pins the fix for the missing
// gccfg.HTTPClient assignment: applyHTTPProxy now routes every provider's
// HTTP client through the configured proxy, including Google. Previously
// Google requests bypassed the proxy entirely, defeating company-wide
// outbound policies.
func TestApplyHTTPProxyAlsoConfiguresGoogle(t *testing.T) {
	cfg := &Config{PersistentConfig: PersistentConfig{HTTPProxy: "http://proxy.example.com:8080"}}
	var (
		accfg anthropic.Config
		gccfg google.Config
		occfg ollama.Config
		ccfg  openai.Config
	)
	require.NoError(t, applyHTTPProxy(cfg, &accfg, &gccfg, &occfg, &ccfg))
	require.NotNil(t, gccfg.HTTPClient,
		"applyHTTPProxy must wire a proxy-aware http.Client for Google too")
	require.Same(t, accfg.HTTPClient, gccfg.HTTPClient,
		"every provider must share the same proxy-configured client")
	require.Same(t, occfg.HTTPClient, gccfg.HTTPClient)
}

// TestApplyHTTPProxyNoopWhenUnset confirms the early-return path still
// leaves every provider's HTTPClient at its zero value when no proxy is
// configured, so providers can fall back to their own DefaultConfig.
func TestApplyHTTPProxyNoopWhenUnset(t *testing.T) {
	cfg := &Config{}
	var (
		accfg anthropic.Config
		gccfg google.Config
		occfg ollama.Config
		ccfg  openai.Config
	)
	require.NoError(t, applyHTTPProxy(cfg, &accfg, &gccfg, &occfg, &ccfg))
	require.Nil(t, gccfg.HTTPClient)
	require.Nil(t, accfg.HTTPClient)
	require.Nil(t, occfg.HTTPClient)
	require.Nil(t, ccfg.HTTPClient)
}
