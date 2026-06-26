package google

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestStreamMessagesIncludesAssistantResponse(t *testing.T) {
	stream := &Stream{
		messages: []proto.Message{
			{Role: proto.RoleUser, Content: "hello"},
		},
		message: "hi there",
	}

	require.Equal(t, []proto.Message{
		{Role: proto.RoleUser, Content: "hello"},
		{Role: proto.RoleAssistant, Content: "hi there"},
	}, stream.Messages())
}

// TestDefaultConfigDoesNotLeakKeyInURL ensures the API key is carried via the
// x-goog-api-key header rather than the request URL, where it would leak via
// proxy logs, error messages, and Referer headers.
func TestDefaultConfigDoesNotLeakKeyInURL(t *testing.T) {
	cfg := DefaultConfig("gemini-2.5-pro", "secret-key-with-&-and-#")

	require.NotContains(t, cfg.BaseURL, "key=")
	require.NotContains(t, cfg.BaseURL, "secret")
	require.Equal(t, "secret-key-with-&-and-#", cfg.AuthToken)
	require.Contains(t, cfg.BaseURL, "streamGenerateContent")
}

func TestDefaultConfigEscapesModelName(t *testing.T) {
	// A model name with characters that require escaping should be encoded.
	cfg := DefaultConfig("models/gemini-2.5-pro", "k")
	require.Contains(t, cfg.BaseURL, "models%2Fgemini-2.5-pro")
}

func TestRequestSendsAPIKeyHeader(t *testing.T) {
	var gotURL string
	var gotAuthHeader string
	var gotAuthQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL = r.URL.String()
		gotAuthHeader = r.Header.Get("x-goog-api-key")
		gotAuthQuery = r.URL.Query().Get("key")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:    server.URL + "/v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse",
		HTTPClient: server.Client(),
		AuthToken:  "test-secret-key",
	}
	client := New(cfg)
	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.Equal(t, "test-secret-key", gotAuthHeader, "API key must be sent in x-goog-api-key header")
	require.Empty(t, gotAuthQuery, "API key must NOT be sent in URL query")
	require.NotContains(t, gotURL, "key=", "URL must not contain the key parameter")
}

func TestRequestOmitsAPIKeyHeaderWhenEmpty(t *testing.T) {
	var headerPresent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headerPresent = r.Header.Get("x-goog-api-key") != ""
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "data: {\"candidates\":[]}\n\n")
	}))
	defer server.Close()

	cfg := Config{
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
		AuthToken:  "",
	}
	client := New(cfg)
	_ = client.Request(context.Background(), proto.Request{
		Messages: []proto.Message{{Role: proto.RoleUser, Content: "hi"}},
	})

	require.False(t, headerPresent, "no api key header should be sent when AuthToken is empty")
}
