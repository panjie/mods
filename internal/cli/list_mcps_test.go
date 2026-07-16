package cli

import (
	"strings"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/stretchr/testify/require"
)

func TestListMCPServersOutputsTransportStatusAndSortedSummary(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	cfg := Config{}
	cfg.MCPServers = map[string]cfgpkg.MCPServerConfig{
		"zeta":  {Type: "sse", URL: "https://example.invalid/sensitive?token=secret"},
		"alpha": {Command: "server", Args: []string{"--token", "secret"}},
		"beta":  {Type: "HTTP", URL: "https://example.invalid/also-sensitive"},
	}

	output := captureStdout(t, func() { listMCPServers(&cfg) })

	require.Contains(t, output, "NAME   TRANSPORT  STATUS")
	require.Contains(t, output, "alpha  stdio      enabled")
	require.Contains(t, output, "beta   http       enabled")
	require.Contains(t, output, "zeta   sse        enabled")
	require.Less(t, strings.Index(output, "alpha"), strings.Index(output, "beta"))
	require.Less(t, strings.Index(output, "beta"), strings.Index(output, "zeta"))
	require.Contains(t, output, "3 MCP servers")
	require.NotContains(t, output, "secret")
	require.NotContains(t, output, "example.invalid")
}

func TestListMCPServersEmptyConfiguration(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	cfg := Config{}
	cfg.MCPServers = map[string]cfgpkg.MCPServerConfig{}

	output := captureStdout(t, func() { listMCPServers(&cfg) })

	require.Equal(t,
		"MCP Servers\n\nNo MCP servers configured.\n\n0 MCP servers\n",
		output,
	)
}
