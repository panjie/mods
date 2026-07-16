package cli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/panjie/mods/internal/tooling"
	"github.com/stretchr/testify/require"
)

func TestListAllToolsOutputsSourcesCapabilitiesDescriptionsAndSummary(t *testing.T) {
	withListOutputTest(t, false, false, 120)
	withToolCatalogTest(t,
		[]tooling.BuiltinToolInfo{
			{Name: "z_write", Description: "Writes a file. More details.", Mutable: true},
			{Name: "a_read", Description: "Reads a file. More details.", ReadOnly: true},
		},
		map[string][]mcp.Tool{
			"z-server": {{Name: "z_mutate", Description: "Mutates data."}},
			"a-server": {{
				Name:        "a_lookup",
				Description: "Looks up data. More details.",
				Annotations: mcp.ToolAnnotation{ReadOnlyHint: mcp.ToBoolPtr(true)},
			}},
		},
		nil,
	)

	output := captureStdout(t, func() {
		require.NoError(t, listAllTools(context.Background(), &Config{}))
	})

	require.Contains(t, output, "NAME      SOURCE    ACCESS  DESCRIPTION")
	require.Contains(t, output, "a_read    built-in  read    Reads a file.")
	require.Contains(t, output, "z_write   built-in  write   Writes a file.")
	require.Contains(t, output, "a_lookup  a-server  read    Looks up data.")
	require.Contains(t, output, "z_mutate  z-server  write   Mutates data.")
	require.Less(t, strings.Index(output, "a_read"), strings.Index(output, "z_write"))
	require.Less(t, strings.Index(output, "z_write"), strings.Index(output, "a_lookup"))
	require.Less(t, strings.Index(output, "a_lookup"), strings.Index(output, "z_mutate"))
	require.Contains(t, output, "4 tools · 2 built-in · 2 MCP")
}

func TestListAllToolsKeepsMCPWarningOnStderr(t *testing.T) {
	withListOutputTest(t, false, false, 100)
	withToolCatalogTest(t,
		[]tooling.BuiltinToolInfo{{Name: "read", Description: "Reads.", ReadOnly: true}},
		nil,
		errors.New("server unavailable"),
	)

	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			require.NoError(t, listAllTools(context.Background(), &Config{}))
		})
	})

	require.Contains(t, stdout, "read  built-in  read")
	require.NotContains(t, stdout, "server unavailable")
	require.Contains(t, stderr, "MCP listing warning: server unavailable")
}

func TestBuiltinCapabilityLabelPrecedence(t *testing.T) {
	require.Equal(t, "interactive", builtinCapabilityLabel(tooling.BuiltinToolInfo{
		Interactive: true, Shell: true, Mutable: true, ReadOnly: true,
	}))
	require.Equal(t, "shell", builtinCapabilityLabel(tooling.BuiltinToolInfo{Shell: true, Mutable: true}))
	require.Equal(t, "write", builtinCapabilityLabel(tooling.BuiltinToolInfo{Mutable: true, ReadOnly: true}))
	require.Equal(t, "read", builtinCapabilityLabel(tooling.BuiltinToolInfo{ReadOnly: true}))
}

func withToolCatalogTest(
	t *testing.T,
	builtins []tooling.BuiltinToolInfo,
	mcpTools map[string][]mcp.Tool,
	mcpErr error,
) {
	t.Helper()
	savedBuiltins := builtinToolSpecs
	savedMCP := listMCPTools
	builtinToolSpecs = func() ([]tooling.BuiltinToolInfo, error) {
		return append([]tooling.BuiltinToolInfo(nil), builtins...), nil
	}
	listMCPTools = func(context.Context, *Config) (map[string][]mcp.Tool, error) {
		return mcpTools, mcpErr
	}
	t.Cleanup(func() {
		builtinToolSpecs = savedBuiltins
		listMCPTools = savedMCP
	})
}
