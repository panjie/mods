// Package copilot implements GitHub Copilot authentication helpers.
package copilot

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

const (
	DefaultClientID = "Iv1.b507a08c87ecfe98"
	// Copilot's token endpoint rejects device-flow tokens that do not carry the
	// same scopes requested by the official language server.
	DefaultDeviceScope = "repo workflow"
	// The official language server sends this preview API version when
	// exchanging GitHub OAuth tokens for Copilot API tokens.
	DefaultGitHubAPIVersion = "2024-12-15"
	DefaultUserAgent        = "GitHubCopilotChat/0.30.0"

	EndpointChatCompletions = "chat-completions"
	EndpointResponses       = "responses"
	EndpointMessages        = "messages"

	DefaultGitHubBaseURL  = "https://github.com"
	DefaultGitHubAPIURL   = "https://api.github.com"
	DefaultCopilotBaseURL = "https://api.githubcopilot.com"

	deviceGrantType = "urn:ietf:params:oauth:grant-type:device_code"
)

type httpDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Client struct {
	BaseURL        string
	APIBaseURL     string
	CopilotBaseURL string
	HTTPClient     httpDoer
	ClientID       string
}

type DeviceCode struct {
	DeviceCode      string
	UserCode        string
	VerificationURI string
	ExpiresIn       time.Duration
	Interval        time.Duration
}

type OAuthToken struct {
	AccessToken string
	TokenType   string
}

type CopilotToken struct {
	Token string
}

type ModelInfo struct {
	ID                 string
	SupportedEndpoints []string
}

func StartDeviceFlow(ctx context.Context, client Client) (DeviceCode, error) {
	form := url.Values{}
	form.Set("client_id", client.clientID())
	form.Set("scope", DefaultDeviceScope)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.githubBaseURL(), "/")+"/login/device/code", strings.NewReader(form.Encode()))
	if err != nil {
		return DeviceCode{}, fmt.Errorf("build device request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	var body struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	if err := doJSON(client.httpClient(), req, &body); err != nil {
		return DeviceCode{}, err
	}
	if body.DeviceCode == "" || body.UserCode == "" || body.VerificationURI == "" {
		return DeviceCode{}, fmt.Errorf("device response missing code")
	}
	interval := time.Duration(body.Interval) * time.Second
	if interval <= 0 {
		interval = 5 * time.Second
	}
	return DeviceCode{
		DeviceCode:      body.DeviceCode,
		UserCode:        body.UserCode,
		VerificationURI: body.VerificationURI,
		ExpiresIn:       time.Duration(body.ExpiresIn) * time.Second,
		Interval:        interval,
	}, nil
}

func PollDeviceFlow(ctx context.Context, client Client, code DeviceCode) (OAuthToken, error) {
	deadline := time.Now().Add(code.ExpiresIn)
	interval := code.Interval
	if interval <= 0 {
		interval = 5 * time.Second
	}
	for {
		form := url.Values{}
		form.Set("client_id", client.clientID())
		form.Set("device_code", code.DeviceCode)
		form.Set("grant_type", deviceGrantType)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(client.githubBaseURL(), "/")+"/login/oauth/access_token", strings.NewReader(form.Encode()))
		if err != nil {
			return OAuthToken{}, fmt.Errorf("build token request: %w", err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		var body struct {
			AccessToken string `json:"access_token"`
			TokenType   string `json:"token_type"`
			Error       string `json:"error"`
		}
		if err := doJSON(client.httpClient(), req, &body); err != nil {
			return OAuthToken{}, err
		}
		if body.AccessToken != "" {
			return OAuthToken{AccessToken: body.AccessToken, TokenType: body.TokenType}, nil
		}
		switch body.Error {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "expired_token", "token_expired":
			return OAuthToken{}, fmt.Errorf("device code expired")
		case "access_denied":
			return OAuthToken{}, fmt.Errorf("device authorization denied")
		case "":
			return OAuthToken{}, fmt.Errorf("token response missing access token")
		default:
			return OAuthToken{}, fmt.Errorf("device authorization failed: %s", body.Error)
		}
		if !deadline.IsZero() && time.Now().Add(interval).After(deadline) {
			return OAuthToken{}, fmt.Errorf("device code expired")
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return OAuthToken{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func ExchangeCopilotToken(ctx context.Context, client Client, githubToken string) (CopilotToken, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.githubAPIBaseURL(), "/")+"/copilot_internal/v2/token", nil)
	if err != nil {
		return CopilotToken{}, fmt.Errorf("build copilot token request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+githubToken)
	for k, v := range dotcomHeaders() {
		req.Header.Set(k, v)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := doJSON(client.httpClient(), req, &body); err != nil {
		return CopilotToken{}, err
	}
	if body.Token == "" {
		return CopilotToken{}, fmt.Errorf("copilot token response missing token")
	}
	return CopilotToken{Token: body.Token}, nil
}

func DiscoverModels(ctx context.Context, client Client, githubToken string) ([]string, error) {
	infos, err := DiscoverModelInfos(ctx, client, githubToken)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(infos))
	for _, info := range infos {
		ids = append(ids, info.ID)
	}
	return ids, nil
}

func DiscoverModelInfos(ctx context.Context, client Client, githubToken string) ([]ModelInfo, error) {
	token, err := ExchangeCopilotToken(ctx, client, githubToken)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(client.copilotBaseURL(), "/")+"/models", nil)
	if err != nil {
		return nil, fmt.Errorf("build models request: %w", err)
	}
	for k, v := range Headers() {
		req.Header.Set(k, v)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token.Token)
	var body struct {
		Data []struct {
			ID                 string   `json:"id"`
			SupportedEndpoints []string `json:"supported_endpoints"`
		} `json:"data"`
	}
	if err := doJSON(client.httpClient(), req, &body); err != nil {
		return nil, err
	}
	infos := make([]ModelInfo, 0, len(body.Data))
	for _, m := range body.Data {
		if m.ID != "" {
			infos = append(infos, ModelInfo{ID: m.ID, SupportedEndpoints: m.SupportedEndpoints})
		}
	}
	if len(infos) == 0 {
		return nil, fmt.Errorf("no models returned")
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].ID < infos[j].ID })
	return infos, nil
}

func SelectEndpoint(info ModelInfo) string {
	for _, endpoint := range info.SupportedEndpoints {
		if endpoint == "/responses" {
			return EndpointResponses
		}
	}
	for _, endpoint := range info.SupportedEndpoints {
		if endpoint == "/v1/messages" {
			return EndpointMessages
		}
	}
	return EndpointChatCompletions
}

func Headers() map[string]string {
	return map[string]string{
		"User-Agent":             DefaultUserAgent,
		"Editor-Version":         "mods/1.0",
		"Editor-Plugin-Version":  "copilot-chat/0.30.0",
		"Copilot-Integration-Id": "vscode-chat",
		"OpenAI-Intent":          "conversation-panel",
	}
}

func dotcomHeaders() map[string]string {
	headers := Headers()
	headers["X-GitHub-Api-Version"] = DefaultGitHubAPIVersion
	headers["Openai-Organization"] = "github-copilot"
	headers["VScode-SessionId"] = randomHexID()
	headers["VScode-MachineId"] = randomHexID()
	return headers
}

func randomHexID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

func doJSON(client httpDoer, req *http.Request, out any) error {
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("network error: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		buf := new(bytes.Buffer)
		_, _ = buf.ReadFrom(resp.Body)
		return httpStatusError(resp.StatusCode, buf.String())
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

func httpStatusError(status int, body string) error {
	body = strings.TrimSpace(body)
	detail := copilotErrorDetail(body)
	if detail != "" {
		if body != "" {
			return fmt.Errorf("API error (HTTP %d): %s", status, detail)
		}
		return fmt.Errorf("API error (HTTP %d): %s", status, detail)
	}
	if body != "" {
		return fmt.Errorf("API error (HTTP %d): %s", status, body)
	}
	return fmt.Errorf("API error (HTTP %d)", status)
}

func copilotErrorDetail(body string) string {
	if body == "" {
		return ""
	}
	var parsed struct {
		Message          string `json:"message"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
		ErrorDetails     struct {
			Message        string `json:"message"`
			NotificationID string `json:"notification_id"`
		} `json:"error_details"`
		UserNotification struct {
			Message string `json:"message"`
			Title   string `json:"title"`
		} `json:"user_notification"`
	}
	if err := json.Unmarshal([]byte(body), &parsed); err != nil {
		return ""
	}
	parts := make([]string, 0, 3)
	for _, value := range []string{
		parsed.ErrorDetails.Message,
		parsed.UserNotification.Message,
		parsed.Message,
		parsed.ErrorDescription,
		parsed.Error,
	} {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
			break
		}
	}
	if id := strings.TrimSpace(parsed.ErrorDetails.NotificationID); id != "" {
		parts = append(parts, "notification_id="+id)
	}
	return strings.Join(parts, " ")
}

func (c Client) clientID() string {
	if c.ClientID != "" {
		return c.ClientID
	}
	return DefaultClientID
}

func (c Client) githubBaseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultGitHubBaseURL
}

func (c Client) githubAPIBaseURL() string {
	if c.APIBaseURL != "" {
		return c.APIBaseURL
	}
	return DefaultGitHubAPIURL
}

func (c Client) copilotBaseURL() string {
	if c.CopilotBaseURL != "" {
		return c.CopilotBaseURL
	}
	return DefaultCopilotBaseURL
}

func (c Client) httpClient() httpDoer {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}
