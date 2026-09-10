package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestDownloadsBatchAndAuthorization(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(r.URL.Path)) }))
	defer server.Close()
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterDownload(registry, FilesystemConfig{Root: root}))
	data, _ := json.Marshal(downloadArgs{Files: []downloadItem{{server.URL + "/a", "a.lua"}, {server.URL + "/b", "中文 b.lua"}}})
	tool, _ := registry.Tool("http_download")
	intent := tool.IntentExtractor(data)
	require.Equal(t, approval.AccessWrite, intent.DominantClass())
	require.Empty(t, intent.WriteOrigins)
	require.Len(t, intent.ReadOrigins, 2)
	out, err := registry.Call(context.Background(), "http_download", data)
	require.NoError(t, err)
	require.Contains(t, out, "completed")
	content, err := os.ReadFile(filepath.Join(root, "a.lua"))
	require.NoError(t, err)
	require.Equal(t, "/a", string(content))
	_, err = registry.Call(context.Background(), "http_download", data)
	require.Error(t, err)
	data, _ = json.Marshal(downloadArgs{Files: []downloadItem{{server.URL + "/replacement", "a.lua"}}, Overwrite: true})
	_, err = registry.Call(context.Background(), "http_download", data)
	require.NoError(t, err)
	content, err = os.ReadFile(filepath.Join(root, "a.lua"))
	require.NoError(t, err)
	require.Equal(t, "/replacement", string(content))
	external := filepath.Join(t.TempDir(), "outside")
	data, _ = json.Marshal(downloadArgs{Files: []downloadItem{{server.URL + "/x", external}}})
	_, err = registry.Call(context.Background(), "http_download", data)
	require.Error(t, err)
	_, err = registry.Call(WithAuthorizedDirs(context.Background(), []string{filepath.Dir(external)}), "http_download", data)
	require.NoError(t, err)
}

func TestDownloadsPartialFailureAndCleanup(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/bad" {
			w.WriteHeader(404)
			return
		}
		if r.URL.Path == "/large" {
			_, _ = w.Write([]byte(strings.Repeat("x", downloadFileLimit+1)))
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()
	root := t.TempDir()
	cfg := FilesystemConfig{Root: root}
	data, _ := json.Marshal(downloadArgs{Files: []downloadItem{{server.URL + "/ok", "a"}, {server.URL + "/bad?token=SECRET", "b"}, {server.URL + "/ok", "c"}}})
	out, err := runDownloads(context.Background(), cfg, data)
	require.Error(t, err)
	var results []downloadResult
	require.NoError(t, json.Unmarshal([]byte(out), &results))
	require.Equal(t, "completed", results[0].Status)
	require.Equal(t, "failed", results[1].Status)
	require.Equal(t, "skipped", results[2].Status)
	require.NotContains(t, out+err.Error(), "SECRET")
	data, _ = json.Marshal(downloadArgs{Files: []downloadItem{{server.URL + "/large", "large"}}})
	_, err = runDownloads(context.Background(), cfg, data)
	require.Error(t, err)
	_, err = os.Stat(filepath.Join(root, "large"))
	require.True(t, os.IsNotExist(err))
	files, err := filepath.Glob(filepath.Join(root, ".mods-download-*"))
	require.NoError(t, err)
	require.Empty(t, files)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = runDownloads(ctx, cfg, data)
	require.Error(t, err)
}

func TestDownloadsNetworkPolicyAndValidation(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", 302) }))
	defer server.Close()
	cfg := FilesystemConfig{Root: t.TempDir()}
	data, _ := json.Marshal(downloadArgs{Files: []downloadItem{{server.URL, "x"}}})
	_, err := runDownloads(context.Background(), cfg, data)
	require.Error(t, err)
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	_, err = runDownloads(context.Background(), cfg, data)
	require.Error(t, err, "redirect cap applies even with private access enabled")
	for _, raw := range []string{`{"files":[]}`, `{"files":[{"url":"https://raw.githubusercontent.com","path":"x"}]}`, `{"files":[{"url":"file:///etc/passwd","path":"x"}]}`, `{"files":[{"url":"https://u:p@example.com/a","path":"x"}]}`} {
		_, err = parseDownloads(cfg.Root, []byte(raw))
		require.Error(t, err)
	}
}
