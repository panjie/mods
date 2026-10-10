package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestRequestSessionSSEOutlivesInitializationTimeout(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	backend := mcpserver.NewMCPServer("test", "1", mcpserver.WithToolCapabilities(false))
	backend.AddTool(mcp.NewTool("echo"), func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("still connected"), nil
	})
	sse := mcpserver.NewSSEServer(backend)
	disconnected := make(chan struct{})
	mcpHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			defer close(disconnected)
		}
		sse.ServeHTTP(w, r)
	}))
	defer mcpHTTP.Close()
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer provider.Close()
	cfg := defaultConfig()
	cfg.API, cfg.Model = "openai", "test"
	cfg.APIs = []API{{Name: "openai", APIKey: "fake", BaseURL: provider.URL + "/", Models: map[string]Model{"test": {Endpoint: "chat-completions"}}}}
	cfg.MCPServers = map[string]cfgpkg.MCPServerConfig{"test": {Type: "sse", URL: mcpHTTP.URL + "/sse"}}
	cfg.MCPTimeout = 200 * time.Millisecond
	cfg.Minimal, cfg.NoSave, cfg.NoInstructions = true, true, true
	cfg.ReviewMode = ReviewNever
	cfg.WorkingDir, cfg.SkillsDirs = t.TempDir(), []string{t.TempDir()}
	m, err := New(context.Background(), &cfg, nil)
	require.NoError(t, err)
	defer m.quit()
	session, err := m.buildRequestSession("echo")
	require.NoError(t, err)
	// Wait past the old registry context deadline, then use the real tool path.
	select {
	case <-disconnected:
		t.Fatal("SSE disconnected during initialization")
	case <-time.After(2 * cfg.MCPTimeout):
	}
	text, err := m.toolCaller(session.cleanup, &cfg)(proto.ToolCallRequest{Name: "test_echo", Arguments: []byte(`{}`)})
	require.NoError(t, err)
	require.Equal(t, "still connected", text)
	// Both request teardown and quitting must release the persistent connection.
	session.runner.close()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("closing the request did not release SSE")
	}
}
