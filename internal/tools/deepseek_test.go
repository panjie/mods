package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/deepseektools"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

type serviceTransport func(*http.Request) (*http.Response, error)

func (f serviceTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func serviceFixture(calls *int, body string) deepseektools.Client {
	return deepseektools.Client{APIKey: "test", HTTP: &http.Client{Transport: serviceTransport(func(r *http.Request) (*http.Response, error) {
		*calls++
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}}
}

func TestDocumentReadPaginationCacheAndInvalidation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "doc.pdf")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0600))
	calls := 0
	r := NewRegistry()
	defer r.Close()
	require.NoError(t, RegisterDocumentRead(r, FilesystemConfig{Root: root}, serviceFixture(&calls, `{"page_count":2,"pages":[{"index":0,"text":"甲乙丙","finish_reason":"stop"},{"index":1,"text":"partial","finish_reason":"length"}]}`)))
	read := func(args string) map[string]any {
		out, err := r.Call(context.Background(), "document_read", json.RawMessage(args))
		require.NoError(t, err)
		var result map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		return result
	}
	a := read(`{"path":"doc.pdf","limit":2}`)
	require.Equal(t, "甲乙", a["text"])
	require.Equal(t, float64(2), a["next_offset"])
	require.Equal(t, false, a["done"])
	a = read(`{"path":"doc.pdf","offset":2}`)
	require.Equal(t, "丙", a["text"])
	require.Equal(t, float64(2), a["next_page"])
	a = read(`{"path":"doc.pdf","page":2}`)
	require.Equal(t, false, a["recognition_complete"])
	require.Equal(t, true, a["done"])
	require.Equal(t, 1, calls)
	for _, args := range []string{`{"path":"doc.pdf","page":3}`, `{"path":"doc.pdf","offset":99}`, `{"path":"doc.pdf","limit":-1}`, `{"path":"doc.pdf","purpose":"invalid"}`, `{"path":"."}`} {
		_, err := r.Call(context.Background(), "document_read", json.RawMessage(args))
		require.Error(t, err)
	}
	require.Equal(t, 1, calls)
	require.NoError(t, os.WriteFile(path, []byte("changed"), 0600))
	read(`{"path":"doc.pdf"}`)
	require.Equal(t, 2, calls)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := r.Call(ctx, "document_read", json.RawMessage(`{"path":"doc.pdf"}`))
	require.Error(t, err)
	require.Equal(t, 2, calls)
}

func TestAudioTranscribeValidation(t *testing.T) {
	root := t.TempDir()
	calls := 0
	r := NewRegistry()
	defer r.Close()
	require.NoError(t, RegisterAudioTranscribe(r, FilesystemConfig{Root: root}, serviceFixture(&calls, `{"text":"你好","duration_ms":1000}`)))
	require.NoError(t, os.WriteFile(filepath.Join(root, "short.wav"), mediafixture.WAV(1), 0600))
	out, err := r.Call(context.Background(), "audio_transcribe", json.RawMessage(`{"path":"short.wav"}`))
	require.NoError(t, err)
	require.Contains(t, out, "你好")
	require.Equal(t, 1, calls)
	require.NoError(t, os.WriteFile(filepath.Join(root, "long.wav"), mediafixture.WAV(121), 0600))
	_, err = r.Call(context.Background(), "audio_transcribe", json.RawMessage(`{"path":"long.wav"}`))
	require.Error(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.wav"), []byte("not audio"), 0600))
	_, err = r.Call(context.Background(), "audio_transcribe", json.RawMessage(`{"path":"bad.wav"}`))
	require.Error(t, err)
	require.Equal(t, 1, calls)
}

func TestDeepSeekFetchPolicyAndPagination(t *testing.T) {
	calls := 0
	r := NewRegistry()
	defer r.Close()
	require.NoError(t, RegisterDeepSeekWebFetch(r, serviceFixture(&calls, `{"title":"test","content":"甲乙丙"}`)))
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "")
	for _, args := range []string{`{"url":"http://127.0.0.1"}`, `{"url":"file:///tmp/x"}`, `{"url":"https://user:pass@example.com"}`, `{"url":"https://example.com","offset":-1}`} {
		_, err := r.Call(context.Background(), "web_fetch", json.RawMessage(args))
		require.Error(t, err)
	}
	require.Zero(t, calls)
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	out, err := r.Call(context.Background(), "web_fetch", json.RawMessage(`{"url":"https://example.com","offset":1,"limit":1}`))
	require.NoError(t, err)
	require.Contains(t, out, `"text":"乙"`)
	require.Contains(t, out, `"next_offset":2`)
	require.Equal(t, 1, calls)
}

func TestDocumentReadAuthorizationAndSizeBeforeUpload(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	path := filepath.Join(outside, "private.pdf")
	require.NoError(t, os.WriteFile(path, []byte("fixture"), 0600))
	calls := 0
	r := NewRegistry()
	defer r.Close()
	require.NoError(t, RegisterDocumentRead(r, FilesystemConfig{Root: root}, serviceFixture(&calls, `{"page_count":1,"pages":[{"index":0,"text":"text","finish_reason":"stop"}]}`)))
	raw, err := json.Marshal(map[string]any{"path": path})
	require.NoError(t, err)
	_, err = r.Call(context.Background(), "document_read", raw)
	require.Error(t, err)
	require.Zero(t, calls)
	ctx := WithAuthorizedDirs(context.Background(), []string{outside})
	_, err = r.Call(ctx, "document_read", raw)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	if err := os.Symlink(path, filepath.Join(root, "escape.pdf")); err == nil {
		_, err = r.Call(context.Background(), "document_read", json.RawMessage(`{"path":"escape.pdf"}`))
		require.Error(t, err)
		require.Equal(t, 1, calls)
	}
	f, err := os.Create(filepath.Join(root, "large.pdf"))
	require.NoError(t, err)
	require.NoError(t, f.Truncate((32<<20)+1))
	require.NoError(t, f.Close())
	_, err = r.Call(context.Background(), "document_read", json.RawMessage(`{"path":"large.pdf"}`))
	require.Error(t, err)
	require.Equal(t, 1, calls)
}
