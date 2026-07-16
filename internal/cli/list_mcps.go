package cli

import (
	"slices"
	"strings"

	"github.com/panjie/mods/internal/mcpclient"
)

func listMCPServers(cfg *Config) {
	rows := make([][]string, 0, len(cfg.MCPServers))
	for name, server := range mcpclient.EnabledServers(cfg) {
		transport := strings.ToLower(strings.TrimSpace(server.Type))
		if transport == "" {
			transport = "stdio"
		}
		rows = append(rows, []string{name, transport, "enabled"})
	}
	slices.SortFunc(rows, func(a, b []string) int {
		return strings.Compare(a[0], b[0])
	})
	printListView(listView{
		Title: "MCP Servers",
		Columns: []listColumn{
			{Header: "NAME"},
			{Header: "TRANSPORT"},
			{Header: "STATUS"},
		},
		Rows:    rows,
		Empty:   "No MCP servers configured.",
		Summary: listCount(len(rows), "MCP server", "MCP servers"),
	})
}
