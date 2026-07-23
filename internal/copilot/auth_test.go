package copilot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStartDeviceFlowRequestsGitHubDeviceCode(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/login/device/code", r.URL.Path)
		require.Equal(t, "POST", r.Method)
		require.Equal(t, "application/json", r.Header.Get("Accept"))
		require.NoError(t, r.ParseForm())
		require.Equal(t, DefaultClientID, r.Form.Get("client_id"))
		require.Equal(t, DefaultDeviceScope, r.Form.Get("scope"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":      "device-123",
			"user_code":        "ABCD-EFGH",
			"verification_uri": "https://github.com/login/device",
			"expires_in":       900,
			"interval":         5,
		})
	}))
	defer srv.Close()

	code, err := StartDeviceFlow(context.Background(), Client{BaseURL: srv.URL, HTTPClient: srv.Client()})
	require.NoError(t, err)
	require.Equal(t, "device-123", code.DeviceCode)
	require.Equal(t, "ABCD-EFGH", code.UserCode)
	require.Equal(t, "https://github.com/login/device", code.VerificationURI)
	require.Equal(t, 900*time.Second, code.ExpiresIn)
	require.Equal(t, 5*time.Second, code.Interval)
}

func TestPollDeviceFlowWaitsUntilAuthorized(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		require.Equal(t, "/login/oauth/access_token", r.URL.Path)
		require.NoError(t, r.ParseForm())
		require.Equal(t, DefaultClientID, r.Form.Get("client_id"))
		require.Equal(t, "device-123", r.Form.Get("device_code"))
		require.Equal(t, deviceGrantType, r.Form.Get("grant_type"))
		if hits == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "github-token",
			"token_type":   "bearer",
		})
	}))
	defer srv.Close()

	token, err := PollDeviceFlow(context.Background(), Client{BaseURL: srv.URL, HTTPClient: srv.Client()}, DeviceCode{
		DeviceCode: "device-123",
		ExpiresIn:  30 * time.Second,
		Interval:   time.Millisecond,
	})
	require.NoError(t, err)
	require.Equal(t, "github-token", token.AccessToken)
	require.Equal(t, 2, hits)
}

func TestExchangeCopilotToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/copilot_internal/v2/token", r.URL.Path)
		require.Equal(t, "Bearer github-token", r.Header.Get("Authorization"))
		require.Equal(t, DefaultGitHubAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		require.Equal(t, DefaultUserAgent, r.Header.Get("User-Agent"))
		require.Equal(t, "github-copilot", r.Header.Get("Openai-Organization"))
		require.NotEmpty(t, r.Header.Get("VScode-SessionId"))
		require.NotEmpty(t, r.Header.Get("VScode-MachineId"))
		_ = json.NewEncoder(w).Encode(map[string]any{"token": "copilot-token"})
	}))
	defer srv.Close()

	token, err := ExchangeCopilotToken(context.Background(), Client{APIBaseURL: srv.URL, HTTPClient: srv.Client()}, "github-token")
	require.NoError(t, err)
	require.Equal(t, "copilot-token", token.Token)
}

func TestExchangeCopilotTokenIncludesForbiddenDetails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, DefaultGitHubAPIVersion, r.Header.Get("X-GitHub-Api-Version"))
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error_details": map[string]any{
				"message":         "Copilot access is disabled by your organization",
				"notification_id": "copilot_disabled",
			},
		})
	}))
	defer srv.Close()

	_, err := ExchangeCopilotToken(context.Background(), Client{APIBaseURL: srv.URL, HTTPClient: srv.Client()}, "github-token")
	require.Error(t, err)
	require.Contains(t, err.Error(), "HTTP 403")
	require.Contains(t, err.Error(), "Copilot access is disabled by your organization")
	require.Contains(t, err.Error(), "copilot_disabled")
}

func TestDiscoverModelsExchangesTokenAndSortsModelIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			require.Equal(t, "Bearer github-token", r.Header.Get("Authorization"))
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "copilot-token"})
		case "/models":
			require.Equal(t, "Bearer copilot-token", r.Header.Get("Authorization"))
			require.Equal(t, DefaultUserAgent, r.Header.Get("User-Agent"))
			require.Equal(t, "conversation-panel", r.Header.Get("OpenAI-Intent"))
			require.Equal(t, "vscode-chat", r.Header.Get("Copilot-Integration-Id"))
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]string{{"id": "gpt-5"}, {"id": "claude-sonnet-4"}},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	ids, err := DiscoverModels(context.Background(), Client{APIBaseURL: srv.URL, CopilotBaseURL: srv.URL, HTTPClient: srv.Client()}, "github-token")
	require.NoError(t, err)
	require.Equal(t, []string{"claude-sonnet-4", "gpt-5"}, ids)
}

func TestDiscoverModelInfosPreservesSupportedEndpoints(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/copilot_internal/v2/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "copilot-token"})
		case "/models":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"id": "gpt-5.4-mini", "supported_endpoints": []string{"/responses", "/chat/completions"}},
					{"id": "claude-sonnet-4", "supported_endpoints": []string{"/v1/messages"}},
					{"id": "gemini-2.5-pro"},
				},
			})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	infos, err := DiscoverModelInfos(context.Background(), Client{APIBaseURL: srv.URL, CopilotBaseURL: srv.URL, HTTPClient: srv.Client()}, "github-token")
	require.NoError(t, err)
	require.Equal(t, []ModelInfo{
		{ID: "claude-sonnet-4", SupportedEndpoints: []string{"/v1/messages"}},
		{ID: "gemini-2.5-pro"},
		{ID: "gpt-5.4-mini", SupportedEndpoints: []string{"/responses", "/chat/completions"}},
	}, infos)
	require.Equal(t, EndpointResponses, SelectEndpoint(infos[2]))
	require.Equal(t, EndpointMessages, SelectEndpoint(infos[0]))
	require.Equal(t, EndpointChatCompletions, SelectEndpoint(infos[1]))
}
