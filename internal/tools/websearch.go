package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/websearch"
)

// RegisterWebSearch registers the native web search tool.
func RegisterWebSearch(registry *Registry, cfg websearch.Config) error {
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "web_search",
			Description: "Search the web for current, up-to-date information. Returns formatted search results with titles, URLs, and snippets.",
			InputSchema: objectSchema(map[string]any{
				"query": stringProp("The search query to look up on the web."),
			}, "query"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			if args.Query == "" {
				return "", fmt.Errorf("websearch: empty search query")
			}
			return websearch.Search(ctx, cfg, args.Query)
		},
	})
}
