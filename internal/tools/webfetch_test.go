package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWebFetchDocumentAndPagination(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/doc", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<html><head><title>配置 &amp; Guide</title></head><body><nav>MENU</nav><main><h1>配置</h1><p>Hello <strong>world</strong> &amp; docs.</p><pre><code>foo
  bar
    baz</code></pre><script>SECRET SCRIPT</script><p hidden>HIDDEN</p><p aria-hidden="true">INVISIBLE</p></main><footer>FOOTER</footer></body></html>`))
	}))
	defer server.Close()
	registry := NewRegistry()
	require.NoError(t, RegisterWebFetch(registry))
	tool, ok := registry.Tool("web_fetch")
	require.True(t, ok)
	require.True(t, tool.Capabilities.ReadOnly)
	var all strings.Builder
	offset := 0
	for {
		data, _ := json.Marshal(map[string]any{"url": server.URL + "/redirect#section", "limit": 7, "offset": offset})
		result, err := registry.Call(context.Background(), "web_fetch", data)
		require.NoError(t, err)
		var page struct {
			URL, Title, Text string
			Next             int `json:"next_offset"`
			Done             bool
		}
		require.NoError(t, json.Unmarshal([]byte(result), &page))
		require.Contains(t, page.URL, "/doc")
		require.Equal(t, "配置 & Guide", page.Title)
		all.WriteString(page.Text)
		if page.Done {
			break
		}
		require.Greater(t, page.Next, offset)
		offset = page.Next
	}
	require.Contains(t, all.String(), "Hello world & docs.")
	require.Contains(t, all.String(), "foo\n  bar\n    baz")
	for _, omitted := range []string{"MENU", "SECRET", "HIDDEN", "INVISIBLE", "FOOTER"} {
		require.NotContains(t, all.String(), omitted)
	}
}

func TestWebFetchLimitsAndErrors(t *testing.T) {
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/large":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(strings.Repeat("x", webFetchLimit+1)))
		case "/binary":
			w.Header().Set("Content-Type", "application/pdf")
			_, _ = w.Write([]byte("PDF"))
		case "/bad":
			w.WriteHeader(http.StatusForbidden)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/credentials":
			http.Redirect(w, r, "http://user:SECRET@example.com/", http.StatusFound)
		case "/empty":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte("<script>app()</script>"))
		default:
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("你好\n  text"))
		}
	}))
	defer server.Close()
	for _, path := range []string{"/large", "/binary", "/bad", "/loop", "/credentials", "/empty"} {
		t.Run(path, func(t *testing.T) {
			data, _ := json.Marshal(map[string]any{"url": server.URL + path + "?token=SECRET"})
			_, err := fetchWebText(context.Background(), data)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "SECRET")
		})
	}
	data, _ := json.Marshal(map[string]any{"url": server.URL})
	out, err := fetchWebText(context.Background(), data)
	require.NoError(t, err)
	require.Contains(t, out, "你好")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = fetchWebText(ctx, data)
	require.Error(t, err)
	t.Setenv("MODS_WEB_SEARCH_ALLOW_PRIVATE", "")
	_, err = fetchWebText(context.Background(), data)
	require.ErrorContains(t, err, "network policy")
	for _, raw := range []string{`{"url":"file:///etc/passwd"}`, `{"url":"https://u:SECRET@example.com"}`, `{"url":"https://example.com","offset":-1}`, `{"url":"https://example.com","limit":20001}`} {
		_, err := fetchWebText(context.Background(), json.RawMessage(raw))
		require.Error(t, err)
		require.NotContains(t, err.Error(), "SECRET")
	}
}

func TestWebFetchHTMLFallback(t *testing.T) {
	for _, html := range []string{`<body><nav>skip</nav><p>body text</p></body>`, `<body>skip<article><p>body text</p></article></body>`} {
		text, _, err := extractWebText(html)
		require.NoError(t, err)
		require.Equal(t, "body text", text)
	}
	text, _, err := extractWebText("<pre>  indented\n    code</pre>")
	require.NoError(t, err)
	require.Equal(t, "  indented\n    code", text)
}
