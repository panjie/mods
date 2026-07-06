package app

import (
	"net/url"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

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

// TestBuildProviderConfigsGoogleUsesUserBaseURL closes the regression that
// originally motivated the fix: an api.BaseURL entry in mods.yml was
// silently ignored for Google, so users targeting a Vertex proxy or a
// reverse-proxy stayed pinned to generativelanguage.googleapis.com.
func TestBuildProviderConfigsGoogleUsesUserBaseURL(t *testing.T) {
	customBase := "https://vertex-proxy.example.com/v1beta/models/{model}:streamGenerateContent?alt=sse"
	mods := &Mods{
		Styles: makeStyles(lipgloss.NewRenderer(nil)),
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
