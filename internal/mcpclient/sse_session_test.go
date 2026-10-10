package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

func TestSSEDiscoveryTimeoutClosesConnection(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	for _, stage := range []string{"endpoint", "initialize", "tools/list"} {
		t.Run(stage, func(t *testing.T) {
			sse := mcpserver.NewSSEServer(mcpserver.NewMCPServer("test", "1", mcpserver.WithToolCapabilities(false)))
			disconnected := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodGet {
					defer close(disconnected)
					if stage == "endpoint" {
						w.Header().Set("Content-Type", "text/event-stream")
						w.WriteHeader(http.StatusOK)
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
				} else {
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
					if request.Method == stage {
						// Accept the request but never publish its JSON-RPC reply.
						w.WriteHeader(http.StatusAccepted)
						return
					}
				}
				sse.ServeHTTP(w, r)
			}))
			defer server.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cfg := &Config{}
			cfg.MCPTimeout = 100 * time.Millisecond
			cfg.MCPServers = map[string]MCPServerConfig{"test": {Type: "sse", URL: server.URL + "/sse"}}
			result := make(chan error, 1)
			go func() {
				session, err := NewToolSession(ctx, cfg)
				if session != nil {
					_ = session.Close()
				}
				result <- err
			}()
			select {
			case err := <-result:
				require.Error(t, err)
			case <-time.After(2 * time.Second):
				t.Fatal("discovery ignored its timeout")
			}
			select {
			case <-disconnected:
			case <-time.After(time.Second):
				t.Fatal("failed discovery leaked the SSE connection")
			}
		})
	}
}

func TestSSESessionCanceledWithParent(t *testing.T) {
	t.Setenv("MODS_MCP_ALLOW_PRIVATE", "1")
	sse := mcpserver.NewSSEServer(mcpserver.NewMCPServer("test", "1", mcpserver.WithToolCapabilities(false)))
	disconnected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			defer close(disconnected)
		}
		sse.ServeHTTP(w, r)
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := &Config{}
	cfg.MCPTimeout = time.Second
	cfg.MCPServers = map[string]MCPServerConfig{"test": {Type: "sse", URL: server.URL + "/sse"}}
	session, err := NewToolSession(ctx, cfg)
	require.NoError(t, err)
	defer session.Close()
	cancel()
	select {
	case <-disconnected:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not release SSE")
	}
	require.NoError(t, session.Close())
	require.NoError(t, session.Close())
}
