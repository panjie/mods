package providerinfo

import (
	"net/url"
	"strings"
)

const OpenCodeGoModelsURL = "https://opencode.ai/inference/go/v1/models"

// ModelRoute describes the service's wire protocol, not model capabilities.
type ModelRoute struct {
	Protocol string
	Endpoint string
}

// Snapshot checked against https://opencode.ai/v2/docs/console/go/#endpoints
// and https://models.dev/api.json on 2026-10-19. Keep IDs exact: neither a
// model family nor its original provider determines a gateway's protocol.
var openCodeGoRoutes = func() map[string]ModelRoute {
	routes := map[string]ModelRoute{}
	for _, id := range []string{
		"gpt-6-luna", "gpt-5.6-luna", "grok-4.7",
		"muse-spark-1.2-contributor", "muse-spark-1.3-contributor",
	} {
		routes[id] = ModelRoute{"openai", "responses"}
	}
	for _, id := range []string{
		"glm-5.3-flash", "glm-5.3", "glm-5.2", "kimi-k3", "kimi-k2.7-code",
		"longcat-2.0", "deepseek-v4.1-flash", "deepseek-v4-pro",
		"mimo-v2.6-flash", "mimo-v2.6-pro", "mimo-v2.5", "mimo-v2.5-pro",
		"hy4-preview", "hy3", "space-bunny", "longcat-2.5-preview-free",
		"step-5-preview", "beam-free",
	} {
		routes[id] = ModelRoute{"openai", "chat-completions"}
	}
	for _, id := range []string{"claude-haiku-5-5", "minimax-m3", "qwen3.8-max", "qwen3.8-flash", "qwen3.7-plus"} {
		routes[id] = ModelRoute{"anthropic", "messages"}
	}
	routes["gemini-4-argon-contributor"] = ModelRoute{"google", ""}
	return routes
}()

// IsOpenCodeGoURL accepts only known official Go roots. In particular, a
// similarly named custom provider, Zen pay-as-you-go URL, or lookalike host
// must not inherit Go routing or have its discovery URL rewritten.
func IsOpenCodeGoURL(baseURL string) bool {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "opencode.ai") ||
		u.User != nil || (u.Port() != "" && u.Port() != "443") || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" {
		return false
	}
	switch strings.TrimRight(u.Path, "/") {
	case "/zen/go/v1", "/zen/go/v1/messages", "/inference/go/v1", "/inference/go/v1/messages",
		"/inference/go/openai/v1", "/inference/go/anthropic/v1", "/inference/go/anthropic/v1/messages":
		return true
	}
	return false
}

func OpenCodeGoRoute(baseURL, model string) (ModelRoute, bool) {
	if !IsOpenCodeGoURL(baseURL) {
		return ModelRoute{}, false
	}
	route, ok := openCodeGoRoutes[strings.TrimSpace(model)]
	return route, ok
}

// OpenCodeHeaders is shared by runtime and setup requests. Callers own the
// session ID so all requests in one conversation reuse the same identity.
func OpenCodeHeaders(baseURL, sessionID string) map[string]string {
	u, err := url.Parse(baseURL)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || !strings.EqualFold(u.Hostname(), "opencode.ai") {
		return nil
	}
	return map[string]string{"x-opencode-session": sessionID, "User-Agent": "mods/1.0"}
}

// OpenAIEndpoint resolves the shared defaults used by runtime and setup.
// Explicit choices always win. This function does no discovery or I/O.
func OpenAIEndpoint(api, model, baseURL, endpoint string) string {
	if api == "chatgpt" {
		return "responses"
	}
	if value := strings.ToLower(strings.TrimSpace(endpoint)); value != "" {
		return value
	}
	if route, ok := OpenCodeGoRoute(baseURL, model); ok && route.Protocol == "openai" {
		return route.Endpoint
	}
	u, err := url.Parse(baseURL)
	if err == nil {
		official := func(name, host string) bool {
			return strings.EqualFold(strings.TrimSpace(api), name) &&
				(strings.TrimSpace(baseURL) == "" || strings.EqualFold(u.Hostname(), host))
		}
		if official("openai", "api.openai.com") {
			return "responses"
		}
		if official("deepseek", "api.deepseek.com") {
			switch strings.ToLower(strings.TrimSpace(model)) {
			case "deepseek-flash", "deepseek-v4-flash", "deepseek-v4-flash-vision-exp", "deepseek-v4-pro":
				return "responses"
			}
		}
	}
	return "chat-completions"
}
