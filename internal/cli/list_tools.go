package cli

import (
	"context"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/tooling"
	"github.com/panjie/mods/internal/ui"
)

// listAllTools prints the full tool catalogue for --list-tools: built-in tools
// first (always available, enumerated offline), then MCP tools grouped by
// server. Built-in tools are annotated with their capability; MCP tools keep
// the existing "server > tool" form. A summary line counts each source.
//
// Built-ins are printed unconditionally so the catalogue is useful even when
// no MCP servers are configured or reachable; an MCP listing error is reported
// as a warning rather than failing the whole command.
func listAllTools(ctx context.Context, cfg *Config) error {
	builtins, err := tooling.BuiltinSpecs()
	if err != nil {
		return err
	}
	mcpServers, mcpErr := mcpclient.Tools(ctx, cfg)

	for _, b := range builtins {
		fmt.Printf("%s (builtin%s)\n", b.Name, builtinCapabilityLabel(b))
	}
	mcpCount := 0
	for sname, tools := range mcpServers {
		for _, t := range tools {
			fmt.Printf("%s > %s (MCP%s)\n", ui.StdoutStyles().Timeago.Render(sname), t.Name, mcpCapabilityLabel(t))
			mcpCount++
		}
	}
	fmt.Printf("\n%d tools (%d MCP / %d built-in)\n", len(builtins)+mcpCount, mcpCount, len(builtins))
	if mcpErr != nil {
		fmt.Printf("MCP listing warning: %v\n", mcpErr)
	}
	return nil
}

// builtinCapabilityLabel renders the capability suffix shown after "builtin".
func builtinCapabilityLabel(b tooling.BuiltinToolInfo) string {
	switch {
	case b.Interactive:
		return ", interactive"
	case b.Shell:
		return ", shell"
	case b.Mutable:
		return ", write"
	case b.ReadOnly:
		return ", read"
	default:
		return ""
	}
}

// mcpCapabilityLabel renders the capability suffix shown after "MCP", mirroring
// the built-in labels. A tool that self-declares readOnlyHint=true is shown as
// "read"; everything else degrades to "write" so users can see at a glance
// which MCP tools will require approval.
func mcpCapabilityLabel(t mcp.Tool) string {
	if mcpclient.IsReadOnly(t) {
		return ", read"
	}
	return ", write"
}
