package ollamaapi

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

type testTransport func(*http.Request) (*http.Response, error)

func (f testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestChatHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		message string
		signin  string
	}{
		{name: "empty service unavailable", status: 503},
		{name: "empty unauthorized", status: 401},
		{name: "JSON error", status: 404, body: `{"error":"model not found"}`, message: "model not found"},
		{name: "plain text error", status: 502, body: "upstream unavailable", message: "upstream unavailable"},
		{name: "authorization URL", status: 401, body: `{"error":"sign in required","signin_url":"https://example.com/signin"}`, signin: "https://example.com/signin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base, err := url.Parse("http://ollama.example")
			require.NoError(t, err)
			body := &trackedBody{Reader: strings.NewReader(tc.body)}
			status := strconv.Itoa(tc.status) + " " + http.StatusText(tc.status)
			client := NewClient(base, &http.Client{Transport: testTransport(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: tc.status, Status: status, Body: body}, nil
			})})
			called := false
			err = client.Chat(context.Background(), &ChatRequest{Model: "test"}, func(ChatResponse) error {
				called = true
				return nil
			})
			require.Error(t, err)
			require.False(t, called)
			require.True(t, body.closed)
			if tc.status == http.StatusUnauthorized {
				var authErr AuthorizationError
				require.ErrorAs(t, err, &authErr)
				require.Equal(t, tc.status, authErr.StatusCode)
				require.Equal(t, status, authErr.Status)
				require.Equal(t, tc.signin, authErr.SigninURL)
			} else {
				var statusErr StatusError
				require.ErrorAs(t, err, &statusErr)
				require.Equal(t, tc.status, statusErr.StatusCode)
				require.Equal(t, status, statusErr.Status)
				require.Equal(t, tc.message, statusErr.ErrorMessage)
			}
		})
	}
}

type trackedBody struct {
	io.Reader
	closed bool
}

func (b *trackedBody) Close() error {
	b.closed = true
	return nil
}
