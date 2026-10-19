package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCheckConnectionUsesSelectedProtocol(t *testing.T) {
	for _, responses := range []bool{false, true} {
		t.Run(fmt.Sprint(responses), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
				require.Equal(t, "setup-session", r.Header.Get("x-opencode-session"))
				require.Equal(t, "mods/1.0", r.UserAgent())
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				require.Equal(t, "opaque-model", body["model"])
				require.Equal(t, true, body["stream"])
				if responses {
					require.Equal(t, "/v1/responses", r.URL.Path)
					require.NotEmpty(t, body["input"])
					require.Equal(t, false, body["store"])
					require.Equal(t, float64(512), body["max_output_tokens"])
					require.NotContains(t, body, "messages")
					writeResponsesSSE(t, w, completedResponseEvent(`[{"type":"message","id":"msg_1","role":"assistant","status":"completed","content":[{"type":"output_text","text":"OK","annotations":[]}]}]`, 2, 1))
				} else {
					require.Equal(t, "/v1/chat/completions", r.URL.Path)
					require.NotEmpty(t, body["messages"])
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprint(w, "data: {\"id\":\"c\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			defer server.Close()
			require.NoError(t, CheckConnection(context.Background(), Config{
				BaseURL: server.URL + "/v1", HTTPClient: server.Client(), AuthToken: "test-key", UseResponses: responses,
				Headers: map[string]string{"x-opencode-session": "setup-session", "User-Agent": "mods/1.0"},
			}, "opaque-model"))
		})
	}
}

func TestCheckConnectionRejectsInvalidOrUnfinishedResponses(t *testing.T) {
	for _, tc := range []struct{ name, payload, want string }{
		{"invalid", `{"ok":true}`, "ended before a terminal event"},
		{"early end", "data: [DONE]\n\n", "ended before a terminal event"},
		{"incomplete", "data: {\"type\":\"response.incomplete\",\"response\":{\"id\":\"r\",\"status\":\"incomplete\",\"output\":[]}}\n\n", "incomplete"},
		{"failed", "data: {\"type\":\"response.failed\",\"response\":{\"id\":\"r\",\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"unavailable\"}}}\n\n", "unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = fmt.Fprint(w, tc.payload)
			}))
			defer server.Close()
			err := CheckConnection(context.Background(), Config{BaseURL: server.URL, HTTPClient: server.Client(), UseResponses: true}, "m")
			require.ErrorContains(t, err, tc.want)
		})
	}
}

func TestCheckConnectionCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	require.Error(t, CheckConnection(ctx, Config{BaseURL: server.URL, HTTPClient: server.Client(), UseResponses: true}, "m"))
}
