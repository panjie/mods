package commandanalysis

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestExtractLiteralRemoteOrigins(t *testing.T) {
	got := extractLiteralRemoteOrigins(`curl -X POST 'HTTPS://user:secret@API.Example.com:443/v1?q=token'; scp file git@GitHub.com:org/repo.git`)
	require.ElementsMatch(t, []string{"https://api.example.com", "ssh://github.com"}, got)
	require.Empty(t, extractLiteralRemoteOrigins(`curl -X POST "$API_URL"`))
	require.Empty(t, extractLiteralRemoteOrigins(`copy C:\temp\file D:\out\file`))
	require.Empty(t, extractLiteralRemoteOrigins(`custom-writer key:value`))
	require.Equal(t, []string{"ssh://example.com"}, extractLiteralRemoteOrigins(`scp file example.com:path/to/file`))
}
