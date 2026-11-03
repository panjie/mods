package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Check the actual provider wire request, including DeepSeek Responses which
// used to replace the configured search service with a hosted search tool.
func TestWebSearchRespectsConfiguredProvider(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	searchRequests := make(chan map[string]any, 8)
	searchServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		searchRequests <- map[string]any{"query": r.URL.Query().Get("q"), "key": r.URL.Query().Get("api_key")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[{"title":"Configured search service","url":"https://example.com","content":"search fixture"}]}`))
	}))
	defer searchServer.Close()
	for _, api := range []string{"deepseek", "openai"} {
		for _, endpoint := range []string{"responses", "chat-completions"} {
			for _, provider := range []string{"tavily", "deepseek", searchServer.URL, "disabled"} {
				t.Run(api+"/"+endpoint+"/"+provider, func(t *testing.T) {
					wire := "responses"
					if endpoint == "chat-completions" {
						wire = "chat"
					}
					requests := make(chan map[string]any, 2)
					execute := provider == searchServer.URL
					round := 0
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						var body map[string]any
						if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
							t.Error(err)
							w.WriteHeader(http.StatusBadRequest)
							return
						}
						requests <- body
						round++
						writeContractResponse(w, wire, execute && round == 1, "web_search", `{"query":"search fixture"}`)
					}))
					defer server.Close()
					cfg := defaultConfig()
					cfg.API, cfg.Model = api, "contract-model"
					cfg.APIs = []API{{Name: api, APIKey: "test-key", BaseURL: server.URL + "/", Models: map[string]Model{cfg.Model: {Endpoint: endpoint}}}}
					cfg.WorkingDir = t.TempDir()
					cfg.SkillsDirs = []string{filepath.Join(cfg.WorkingDir, "skills")}
					cfg.NoSave, cfg.NoInstructions = true, true
					cfg.WebSearch, cfg.WebSearchProvider = provider != "disabled", provider
					cfg.WebSearchAPIKey, cfg.MCPServers = "search-test-key", nil
					cfg.BuiltinTools.Filesystem, cfg.BuiltinTools.Shell = FilesystemNever, false
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					m, err := New(ctx, &cfg, nil)
					require.NoError(t, err)
					defer m.quit()
					session, err := m.buildRequestSession("Search for search fixture.")
					require.NoError(t, err)
					defer session.runner.close()
					drainContractStream(t, session.stream)
					request := <-requests
					found := 0
					for _, raw := range request["tools"].([]any) {
						tool := raw.(map[string]any)
						require.NotEqual(t, "web_search", tool["type"], "must never substitute hosted search")
						definition := tool
						if wire == "chat" {
							definition = tool["function"].(map[string]any)
						}
						if definition["name"] == "web_search" {
							found++
							require.Equal(t, "function", tool["type"])
							parameters := definition["parameters"].(map[string]any)
							require.Contains(t, parameters["properties"], "query")
						}
					}
					if cfg.WebSearch {
						require.Equal(t, 1, found)
					} else {
						require.Zero(t, found)
					}
					if execute {
						statuses := session.stream.CallTools()
						require.Len(t, statuses, 1)
						require.NoError(t, statuses[0].Err)
						require.Contains(t, statuses[0].Output, "Configured search service")
						select {
						case search := <-searchRequests:
							require.Equal(t, "search fixture", search["query"])
							require.Equal(t, "search-test-key", search["key"])
						default:
							t.Fatal("configured search provider was not called")
						}
						require.Equal(t, "contract-done", drainContractStream(t, session.stream))
					}
				})
			}
		}
	}
}
