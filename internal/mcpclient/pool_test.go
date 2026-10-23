package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	mcpserver "github.com/mark3labs/mcp-go/server"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestChatPoolReusesTransportRefreshesPermissionsAndCloses(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	backend := mcpserver.NewMCPServer("test", "1", mcpserver.WithToolCapabilities(false))
	handler := func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return mcp.NewToolResultText("ok"), nil
	}
	backend.AddTool(mcp.NewTool("echo", mcp.WithReadOnlyHintAnnotation(true)), handler)
	sse := mcpserver.NewSSEServer(backend)
	var connections atomic.Int32
	var active atomic.Int32
	var failCalls atomic.Bool
	var failedCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			connections.Add(1)
			active.Add(1)
			defer active.Add(-1)
		}
		if r.Method == http.MethodPost && failCalls.Load() {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(data))
			var request struct{ Method string }
			if err := json.Unmarshal(data, &request); err != nil {
				t.Error(err)
				return
			}
			if request.Method == "tools/call" {
				failedCalls.Add(1)
				http.Error(w, "transport failed", http.StatusServiceUnavailable)
				return
			}
		}
		sse.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, closePool := WithSessionPool(context.Background())
	defer closePool()
	cfg := &Config{}
	cfg.MCPTimeout = time.Second
	cfg.MCPServers = map[string]MCPServerConfig{"test": {Type: "sse", URL: server.URL + "/sse"}}
	turn, cancel := context.WithCancel(ctx)
	registry := toolregistry.NewRegistry()
	require.NoError(t, RegisterTools(turn, cfg, registry))
	tool, ok := registry.Tool("test_echo")
	require.True(t, ok)
	require.True(t, tool.Capabilities.ReadOnly)
	require.NoError(t, registry.Close())
	cancel()
	require.EqualValues(t, 1, active.Load())
	// Permission metadata must be refreshed on the retained connection.
	backend.AddTool(mcp.NewTool("echo", mcp.WithReadOnlyHintAnnotation(false)), handler)
	registry = toolregistry.NewRegistry()
	require.NoError(t, RegisterTools(ctx, cfg, registry))
	tool, ok = registry.Tool("test_echo")
	require.True(t, ok)
	require.False(t, tool.Capabilities.ReadOnly)
	require.EqualValues(t, 1, connections.Load())
	text, err := registry.Call(ctx, "test_echo", []byte(`{}`))
	require.NoError(t, err)
	require.Equal(t, "ok", text)
	// Fail a real call at the transport. Never automatically replay a possibly
	// effectful call, even though a future turn may reconnect.
	failCalls.Store(true)
	_, err = registry.Call(ctx, "test_echo", []byte(`{}`))
	require.Error(t, err)
	require.EqualValues(t, 1, failedCalls.Load())
	require.NoError(t, registry.Close())
	registry = toolregistry.NewRegistry()
	require.NoError(t, RegisterTools(ctx, cfg, registry))
	require.EqualValues(t, 2, connections.Load())
	require.EqualValues(t, 1, failedCalls.Load())
	require.NoError(t, registry.Close())
	changed := cfg.MCPServers["test"]
	changed.Env = []string{"MODS_POOL_TEST=changed"}
	cfg.MCPServers["test"] = changed
	registry = toolregistry.NewRegistry()
	require.NoError(t, RegisterTools(ctx, cfg, registry))
	require.EqualValues(t, 3, connections.Load())
	require.NoError(t, registry.Close())
	require.NoError(t, closePool())
	require.Eventually(t, func() bool { return active.Load() == 0 }, time.Second, time.Millisecond)
	require.NoError(t, closePool())
}

func TestPoolCanceledDiscoveryDoesNotRetainTransport(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	connected := make(chan struct{})
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		close(connected)
		<-r.Context().Done()
		close(disconnected)
	}))
	defer server.Close()
	ctx, closePool := WithSessionPool(context.Background())
	defer closePool()
	cfg := &Config{}
	cfg.MCPTimeout = time.Second
	cfg.MCPServers = map[string]MCPServerConfig{"test": {Type: "sse", URL: server.URL + "/sse"}}
	turn, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- RegisterTools(turn, cfg, toolregistry.NewRegistry()) }()
	select {
	case <-connected:
	case <-time.After(time.Second):
		t.Fatal("not connected")
	}
	cancel()
	select {
	case err := <-result:
		require.Error(t, err)
	case <-time.After(time.Second):
		t.Fatal("discovery did not cancel")
	}
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("transport leaked")
	}
	require.Nil(t, ctx.Value(poolKey{}).(*sessionPool).session)
}
