package cli

import (
	"context"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/tooling"
)

var builtinToolSpecs = tooling.BuiltinSpecs
var listMCPTools = mcpclient.Tools

// listAllTools prints the full tool catalogue for --list-tools: built-in tools
// first (always available, enumerated offline), then MCP tools grouped by
// server. Each row includes its source, capability, and a short description;
// a summary line counts each source.
//
// Built-ins are printed unconditionally so the catalogue is useful even when
// no MCP servers are configured or reachable; an MCP listing error is reported
// as a warning rather than failing the whole command.
func listAllTools(ctx context.Context, cfg *Config) error {
	builtins, err := builtinToolSpecs()
	if err != nil {
		return err
	}
	mcpServers, mcpErr := listMCPTools(ctx, cfg)

	slices.SortFunc(builtins, func(a, b tooling.BuiltinToolInfo) int {
		return strings.Compare(a.Name, b.Name)
	})
	rows := make([][]string, 0, len(builtins))
	for _, b := range builtins {
		rows = append(rows, []string{
			b.Name,
			"built-in",
			builtinCapabilityLabel(b),
			firstSentence(normalizeListDescription(b.Description)),
		})
	}
	mcpCount := 0
	serverNames := slices.Collect(maps.Keys(mcpServers))
	slices.Sort(serverNames)
	for _, sname := range serverNames {
		tools := mcpServers[sname]
		slices.SortFunc(tools, func(a, b mcp.Tool) int {
			return strings.Compare(a.Name, b.Name)
		})
		for _, t := range tools {
			rows = append(rows, []string{
				t.Name,
				sname,
				mcpCapabilityLabel(t),
				firstSentence(normalizeListDescription(t.Description)),
			})
			mcpCount++
		}
	}
	printListView(listView{
		Title: "Tools",
		Columns: []listColumn{
			{Header: "NAME"},
			{Header: "SOURCE"},
			{Header: "ACCESS"},
			{Header: "DESCRIPTION", Flexible: true},
		},
		Rows:  rows,
		Empty: "No tools found.",
		Summary: fmt.Sprintf(
			"%s · %d built-in · %d MCP",
			listCount(len(rows), "tool", "tools"),
			len(builtins),
			mcpCount,
		),
	})
	if mcpErr != nil {
		fmt.Fprintf(os.Stderr, "MCP listing warning: %v\n", mcpErr)
	}
	return nil
}

// builtinCapabilityLabel renders the access value for a built-in tool.
func builtinCapabilityLabel(b tooling.BuiltinToolInfo) string {
	switch {
	case b.Interactive:
		return "interactive"
	case b.Shell:
		return "shell"
	case b.Mutable:
		return "write"
	case b.ReadOnly:
		return "read"
	default:
		return ""
	}
}

// mcpCapabilityLabel renders the access value for an MCP tool, mirroring the
// built-in labels. A tool that self-declares readOnlyHint=true is shown as
// "read"; everything else degrades to "write" so users can see at a glance
// which MCP tools will require approval.
func mcpCapabilityLabel(t mcp.Tool) string {
	if mcpclient.IsReadOnly(t) {
		return "read"
	}
	return "write"
}
