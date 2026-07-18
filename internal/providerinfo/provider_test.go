package providerinfo

import (
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
