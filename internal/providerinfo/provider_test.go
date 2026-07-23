package providerinfo

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtocolResolution(t *testing.T) {
	require.Equal(t, "anthropic", Protocol("custom", "anthropic"))
	require.Equal(t, "google", Protocol("google", ""))
	require.Equal(t, "github-copilot", Protocol("github-copilot", ""))
	require.Equal(t, "openai", Protocol("custom", ""))
	require.Equal(t, "openai", Protocol("custom", "typo"))
	require.False(t, KnownProtocol("azure-ad"))
	require.True(t, KnownProtocol("github-copilot"))
	require.False(t, KnownProtocol("typo"))
}

func TestSharedProviderMetadata(t *testing.T) {
	google, ok := Lookup("google")
	require.True(t, ok)
	require.Contains(t, google.DefaultBaseURL, "{model}")
	require.Equal(t, "GOOGLE_API_KEY", google.APIKeyEnv)
	require.NotEmpty(t, google.Description)
	copilot, ok := Lookup("github-copilot")
	require.True(t, ok)
	require.Equal(t, "github-copilot", copilot.Protocol)
	require.Equal(t, "https://api.githubcopilot.com", copilot.DefaultBaseURL)
	_, ok = Lookup("azure-ad")
	require.False(t, ok)
	require.Equal(t, "https://your-server.com/v1", DefaultBaseURL("custom"))
}

func TestProviderDescriptionsIdentifyProviders(t *testing.T) {
	expected := map[string]string{
		"anthropic":      "Anthropic API",
		"azure":          "Azure OpenAI",
		"deepseek":       "DeepSeek API",
		"github-copilot": "GitHub Copilot",
		"glm":            "Zhipu AI",
		"google":         "Google AI",
		"kimi":           "Moonshot AI",
		"minimax":        "MiniMax API",
		"ollama":         "Local model runtime (no API key needed)",
		"openai":         "OpenAI API",
		"openrouter":     "Multi-provider API gateway",
		"qwen":           "Alibaba Cloud",
	}

	require.Len(t, Descriptors(), len(expected))
	for name, want := range expected {
		provider, ok := Lookup(name)
		require.True(t, ok, name)
		require.Equal(t, want, provider.Description, name)
	}
}

func TestProviderCatalogIsStableAndDrivesKnownProtocols(t *testing.T) {
	catalog := Descriptors()
	names := make([]string, 0, len(catalog))
	for _, provider := range catalog {
		names = append(names, provider.Name)
		lookedUp, ok := Lookup(provider.Name)
		require.True(t, ok)
		require.Equal(t, lookedUp, provider.Descriptor)
	}
	require.True(t, slices.IsSorted(names))

	protocols := Protocols()
	require.NotEmpty(t, protocols)
	for _, protocol := range protocols {
		require.True(t, KnownProtocol(protocol), protocol)
	}
	protocols[0] = "mutated"
	require.NotEqual(t, protocols, Protocols())
}
