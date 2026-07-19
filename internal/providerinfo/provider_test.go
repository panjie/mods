package providerinfo

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtocolResolution(t *testing.T) {
	require.Equal(t, "anthropic", Protocol("custom", "anthropic"))
	require.Equal(t, "google", Protocol("google", ""))
	require.Equal(t, "openai", Protocol("custom", ""))
	require.Equal(t, "openai", Protocol("custom", "typo"))
	require.True(t, KnownProtocol("azure-ad"))
	require.False(t, KnownProtocol("typo"))
}

func TestSharedProviderMetadata(t *testing.T) {
	google, ok := Lookup("google")
	require.True(t, ok)
	require.Contains(t, google.DefaultBaseURL, "{model}")
	require.Equal(t, "GOOGLE_API_KEY", google.APIKeyEnv)
	require.NotEmpty(t, google.Description)
	require.Equal(t, "https://your-server.com/v1", DefaultBaseURL("custom"))
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
