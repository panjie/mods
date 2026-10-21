//go:build integration

package websearch_test

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestDeepSeekSearchIntegration makes one real, potentially billable search
// request through the native tool, using only a public, non-private query.
func TestDeepSeekSearchIntegration(t *testing.T) {
	if strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY")) == "" {
		t.Skip("DEEPSEEK_API_KEY is required")
	}
	cfg := config.Default()
	require.NoError(t, yaml.Unmarshal([]byte("web-search: true\nweb-search-provider: deepseek\nweb-search-api-key-env: DEEPSEEK_API_KEY\n"), &cfg))
	cfg.ApplyDefaults()
	registry := tools.NewRegistry()
	require.NoError(t, tools.RegisterWebSearch(registry, websearch.Config{
		Enabled: cfg.WebSearch, Provider: cfg.WebSearchProvider,
		APIKey: cfg.WebSearchAPIKey, MaxResults: 3,
	}))
	const query = "DeepSeek 网页搜索 API site:api-docs.deepseek.com"
	args, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start := time.Now()
	output, err := registry.Call(ctx, "web_search", args)
	if err != nil {
		t.Fatalf("live search failed: %s", strings.ReplaceAll(err.Error(), cfg.WebSearchAPIKey, "[REDACTED]"))
	}
	if strings.Contains(output, cfg.WebSearchAPIKey) {
		t.Fatal("search output unexpectedly contains the API key")
	}
	require.Contains(t, output, query)
	// Each result must include a title, an HTTP(S) URL, and a nonempty summary.
	results := regexp.MustCompile(`(?m)^\d+\. \S[^\n]*\n   URL: (https?://\S+)\n   \S`).FindAllStringSubmatch(output, -1)
	require.NotEmpty(t, results, "expected formatted titles, URLs, and summaries")
	require.Len(t, results, strings.Count(output, "\n   URL: "))
	require.LessOrEqual(t, len(results), 3)
	t.Logf("query=%q results=%d output_bytes=%d elapsed=%s", query, len(results), len(output), time.Since(start).Round(time.Millisecond))
	for _, result := range results {
		t.Logf("source: %s", result[1])
	}
}
