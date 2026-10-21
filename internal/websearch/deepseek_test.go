package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type deepSeekTransport func(*http.Request) (*http.Response, error)

func (f deepSeekTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func deepSeekTestClient(t *testing.T, status int, body string, check func(*http.Request)) *http.Client {
	t.Helper()
	return &http.Client{Transport: deepSeekTransport(func(req *http.Request) (*http.Response, error) {
		if check != nil {
			check(req)
		}
		return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
}

func TestDeepSeekSearchRequestAndResults(t *testing.T) {
	query := `深度求索 site:github.com OR site:deepseek.com lang:zh after:2026`
	client := deepSeekTestClient(t, http.StatusOK, `{"results":[
		{"title":"官方公告","url":"https://deepseek.com/news","summary":"完整摘要","snippet":"短摘要","content":"不应返回的正文","date":null,"site_name":null,"rerank_score":2.4},
		{"title":"Fallback","url":"https://example.com","summary":"","snippet":"短摘要"},
		{"title":"Whitespace","url":"https://example.com/2","summary":"  ","snippet":"备用摘要"}
	]}`, func(req *http.Request) {
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, "https://api.deepseek.com/v1/tools/search", req.URL.String())
		require.Equal(t, "Bearer test-key", req.Header.Get("Authorization"))
		require.Equal(t, "application/json", req.Header.Get("Content-Type"))
		var body map[string]any
		require.NoError(t, json.NewDecoder(req.Body).Decode(&body))
		require.Equal(t, map[string]any{"query": query, "count": float64(5), "need_content": false}, body)
	})
	results, err := searchDeepSeek(context.Background(), client, "test-key", query, 5)
	require.NoError(t, err)
	require.Equal(t, []Result{
		{Title: "官方公告", URL: "https://deepseek.com/news", Snippet: "完整摘要"},
		{Title: "Fallback", URL: "https://example.com", Snippet: "短摘要"},
		{Title: "Whitespace", URL: "https://example.com/2", Snippet: "备用摘要"},
	}, results)
	output := formatResults(query, results)
	require.Contains(t, output, "完整摘要")
	require.Contains(t, output, "https://deepseek.com/news")
	require.NotContains(t, output, "不应返回的正文")
}

func TestDeepSeekSearchValidation(t *testing.T) {
	client := deepSeekTestClient(t, http.StatusOK, `{"results":[]}`, func(*http.Request) {
		t.Error("invalid input must not send a request")
	})
	for _, query := range []string{"", " \t\n", strings.Repeat("中", 129)} {
		_, err := searchDeepSeek(context.Background(), client, "key", query, 5)
		require.ErrorContains(t, err, "1 to 128 Unicode characters")
	}
	for _, count := range []int{0, 51} {
		_, err := searchDeepSeek(context.Background(), client, "key", "query", count)
		require.ErrorContains(t, err, "between 1 and 50")
	}
	// The limit counts Unicode characters, not UTF-8 bytes.
	client = deepSeekTestClient(t, http.StatusOK, `{"results":[]}`, nil)
	for _, count := range []int{1, 50} {
		results, err := searchDeepSeek(context.Background(), client, "key", strings.Repeat("中", 128), count)
		require.NoError(t, err)
		require.Empty(t, results)
	}
}

func TestDeepSeekSearchErrors(t *testing.T) {
	for _, status := range []int{400, 401, 402, 429, 500, 504} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			client := deepSeekTestClient(t, status, `{"error":{"message":"provider failure","type":"api_error"}}`, nil)
			_, err := searchDeepSeek(context.Background(), client, "key", "query", 5)
			require.ErrorContains(t, err, fmt.Sprintf("searching DeepSeek: HTTP %d", status))
			require.ErrorContains(t, err, "provider failure")
		})
	}
	t.Run("invalid JSON", func(t *testing.T) {
		client := deepSeekTestClient(t, http.StatusOK, `<html>unavailable</html>`, nil)
		_, err := searchDeepSeek(context.Background(), client, "key", "query", 5)
		require.ErrorContains(t, err, "searching DeepSeek:")
	})
	t.Run("bounded error body", func(t *testing.T) {
		client := deepSeekTestClient(t, 500, strings.Repeat("中", 1000), nil)
		_, err := searchDeepSeek(context.Background(), client, "key", "query", 5)
		require.Error(t, err)
		require.Less(t, len(err.Error()), 250)
	})
	t.Run("transport failure", func(t *testing.T) {
		failure := errors.New("network unavailable")
		client := &http.Client{Transport: deepSeekTransport(func(*http.Request) (*http.Response, error) {
			return nil, failure
		})}
		_, err := searchDeepSeek(context.Background(), client, "key", "query", 5)
		require.ErrorIs(t, err, failure)
	})
	t.Run("context cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		client := &http.Client{Transport: deepSeekTransport(func(req *http.Request) (*http.Response, error) {
			return nil, req.Context().Err()
		})}
		_, err := searchDeepSeek(ctx, client, "key", "query", 5)
		require.ErrorIs(t, err, context.Canceled)
	})
}
