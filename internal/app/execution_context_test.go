package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestDownloadToolCallerUsesOwnTimeoutAndHonorsCancellation(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded"))
	}))
	defer server.Close()
	cfg := defaultConfig()
	cfg.WorkingDir = t.TempDir()
	cfg.ReviewMode = ReviewNever
	cfg.MCPTimeout = time.Nanosecond
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	m := &Mods{Config: &cfg, ctx: ctx, reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterDownload(registry, toolregistry.FilesystemConfig{Root: cfg.WorkingDir}))
	data, err := json.Marshal(map[string]any{"files": []map[string]string{{"url": server.URL, "path": "test.txt"}}})
	require.NoError(t, err)
	caller := m.toolCaller(registry, &cfg)
	out, err := caller(proto.ToolCallRequest{Name: "http_download", Arguments: data})
	require.NoError(t, err)
	require.Contains(t, out, `"completed"`)
	content, err := os.ReadFile(filepath.Join(cfg.WorkingDir, "test.txt"))
	require.NoError(t, err)
	require.Equal(t, "downloaded", string(content))
	cancel()
	data, err = json.Marshal(map[string]any{"files": []map[string]string{{"url": server.URL, "path": "canceled.txt"}}})
	require.NoError(t, err)
	_, err = caller(proto.ToolCallRequest{Name: "http_download", Arguments: data})
	require.Error(t, err)
	require.NoFileExists(t, filepath.Join(cfg.WorkingDir, "canceled.txt"))
}
