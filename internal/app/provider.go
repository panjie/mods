package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/copilot"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/stream"
)

var exchangeCopilotToken = func(githubToken string) (string, error) {
	tok, err := copilot.ExchangeCopilotToken(context.Background(), copilot.Client{}, githubToken)
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

type providerConfigs struct {
	Anthropic anthropic.Config
	Google    google.Config
	Ollama    ollama.Config
	OpenAI    openai.Config
}

type resolvedProvider struct {
	Name     string
	Protocol string
	Profile  openai.ProviderProfile
	Endpoint string
	BaseURL  string
}

func resolveProvider(mod Model, api API) (resolvedProvider, error) {
	resolved := resolvedProvider{
		Name: mod.API, Protocol: modelProtocol(mod), Profile: modelProviderProfile(mod),
	}
	if err := validateEndpointForProtocol(resolved.Protocol, mod.Endpoint); err != nil {
		return resolved, err
	}
	baseURL, err := resolvedProviderBaseURL(api)
	if err != nil {
		return resolved, err
	}
	resolved.BaseURL = baseURL
	switch resolved.Protocol {
	case "openai":
		resolved.Endpoint = normalizedEndpointName(useResponsesEndpoint(mod, baseURL))
	case "github-copilot":
		if strings.EqualFold(strings.TrimSpace(mod.Endpoint), copilot.EndpointMessages) {
			resolved.Endpoint = copilot.EndpointMessages
		} else {
			resolved.Endpoint = normalizedEndpointName(useCopilotResponses(mod))
		}
	default:
		resolved.Endpoint = resolved.Protocol
	}
	if resolved.Endpoint == copilot.EndpointResponses &&
		resolved.Profile != openai.ProviderProfileOpenAI && resolved.Profile != openai.ProviderProfileDeepSeek {
		return resolved, fmt.Errorf("endpoint responses is not implemented for provider-profile %s", resolved.Profile)
	}
	return resolved, nil
}

func (m *Mods) buildProviderConfigs(mod Model, api API) (providerConfigs, error) {
	var cfgs providerConfigs
	resolved, err := resolveProvider(mod, api)
	if err != nil {
		return cfgs, modsError{Err: err, ReasonText: "Invalid provider routing configuration"}
	}
	debug.Printf(
		"Provider route: provider=%s protocol=%s profile=%s endpoint=%s base_url=%s",
		resolved.Name, resolved.Protocol, resolved.Profile, resolved.Endpoint, safeBaseURLForDebug(resolved.BaseURL),
	)
	keyEnv, keyURL := providerinfo.Auth(mod.API)
	switch resolved.Protocol {
	case "ollama":
		cfgs.Ollama = ollama.DefaultConfig()
		if resolved.BaseURL != "" {
			cfgs.Ollama.BaseURL = resolved.BaseURL
		}
	case "anthropic":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Anthropic authentication failed"}
		}
		cfgs.Anthropic = anthropic.DefaultConfig(key)
		if resolved.BaseURL != "" {
			cfgs.Anthropic.BaseURL = resolved.BaseURL
		}
	case "google":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Google authentication failed"}
		}
		cfgs.Google = google.DefaultConfig(mod.Name, key)
		cfgs.Google.ThinkingBudget = mod.ThinkingBudget
		if resolved.BaseURL != "" {
			cfgs.Google.BaseURL = applyGoogleBaseURLOverride(resolved.BaseURL, mod.Name)
		}
	case "azure":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Azure authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:       key,
			BaseURL:         resolved.BaseURL,
			ProviderProfile: openai.ProviderProfileOpenAI,
			ExtraParams:     cloneAnyMap(mod.ExtraParams),
			ThoughtFields:   mod.ThinkFields,
			ThinkTag:        mod.ThinkTag,
		}
		cfgs.OpenAI.APIType = "azure"
	case "github-copilot":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "GitHub Copilot authentication failed"}
		}
		copilotToken, err := exchangeCopilotToken(key)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "GitHub Copilot authentication failed"}
		}
		if strings.EqualFold(strings.TrimSpace(mod.Endpoint), copilot.EndpointMessages) {
			return cfgs, modsError{
				ReasonText: fmt.Sprintf("GitHub Copilot model %s requires /v1/messages, which is not supported yet", mod.Name),
			}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:       copilotToken,
			BaseURL:         resolved.BaseURL,
			UseResponses:    resolved.Endpoint == copilot.EndpointResponses,
			ProviderProfile: openai.ProviderProfileOpenAI,
			Headers:         copilot.Headers(),
			ExtraParams:     cloneAnyMap(mod.ExtraParams),
			ThoughtFields:   mod.ThinkFields,
			ThinkTag:        mod.ThinkTag,
		}
	default:
		if err := validateResponsesEndpoint(mod.Endpoint); err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Invalid model endpoint configuration"}
		}
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "OpenAI authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:        key,
			BaseURL:          resolved.BaseURL,
			UseResponses:     resolved.Endpoint == copilot.EndpointResponses,
			ProviderProfile:  resolved.Profile,
			ResponsesProfile: resolved.Profile,
			ExtraParams:      cloneAnyMap(mod.ExtraParams),
			ThoughtFields:    mod.ThinkFields,
			ThinkTag:         mod.ThinkTag,
		}
	}
	return cfgs, nil
}

func modelProtocol(mod Model) string {
	if value := strings.ToLower(strings.TrimSpace(mod.Protocol)); value != "" {
		return value
	}
	return providerinfo.Protocol(mod.API, "")
}

func modelProviderProfile(mod Model) openai.ProviderProfile {
	return openai.ProviderProfile(providerinfo.Profile(mod.API, "", mod.ProviderProfile))
}

func resolvedProviderBaseURL(api API) (string, error) {
	if value := strings.TrimSpace(api.BaseURL); value != "" {
		return value, nil
	}
	if descriptor, ok := providerinfo.Lookup(api.Name); ok && descriptor.DefaultBaseURL != "" {
		return descriptor.DefaultBaseURL, nil
	}
	return "", fmt.Errorf("provider %q requires base-url; only built-in providers with an official endpoint may omit it", api.Name)
}

func cloneAnyMap(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		switch typed := value.(type) {
		case map[string]any:
			dst[key] = cloneAnyMap(typed)
		case []any:
			items := make([]any, len(typed))
			for i := range typed {
				if nested, ok := typed[i].(map[string]any); ok {
					items[i] = cloneAnyMap(nested)
				} else {
					items[i] = typed[i]
				}
			}
			dst[key] = items
		default:
			dst[key] = value
		}
	}
	return dst
}

func validateResponsesEndpoint(endpoint string) error {
	switch strings.ToLower(strings.TrimSpace(endpoint)) {
	case "", copilot.EndpointResponses, copilot.EndpointChatCompletions:
		return nil
	default:
		return fmt.Errorf("endpoint %q is invalid: expected responses or chat-completions", endpoint)
	}
}

func validateEndpointForProtocol(protocol, endpoint string) error {
	value := strings.ToLower(strings.TrimSpace(endpoint))
	if value == "" {
		return nil
	}
	switch protocol {
	case "openai":
		return validateResponsesEndpoint(value)
	case "github-copilot":
		switch value {
		case copilot.EndpointResponses, copilot.EndpointChatCompletions, copilot.EndpointMessages:
			return nil
		}
	case "azure":
		if value == copilot.EndpointChatCompletions {
			return nil
		}
		if value == copilot.EndpointResponses {
			return fmt.Errorf("endpoint responses is not supported by the Azure adapter")
		}
	default:
		return fmt.Errorf("endpoint %q is not valid for %s protocol; endpoint is only configurable for OpenAI-compatible providers", endpoint, protocol)
	}
	return fmt.Errorf("endpoint %q is invalid for %s protocol", endpoint, protocol)
}

func normalizedEndpointName(useResponses bool) string {
	if useResponses {
		return copilot.EndpointResponses
	}
	return copilot.EndpointChatCompletions
}

func safeBaseURLForDebug(value string) string {
	u, err := url.Parse(value)
	if err != nil {
		return "(invalid)"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func useCopilotResponses(mod Model) bool {
	switch strings.ToLower(strings.TrimSpace(mod.Endpoint)) {
	case copilot.EndpointResponses:
		return true
	case copilot.EndpointChatCompletions, "":
		return strings.HasPrefix(strings.ToLower(mod.Name), "gpt-5")
	default:
		return false
	}
}

func useResponsesEndpoint(mod Model, baseURL string) bool {
	switch strings.ToLower(strings.TrimSpace(mod.Endpoint)) {
	case copilot.EndpointResponses:
		return true
	case copilot.EndpointChatCompletions:
		return false
	}
	if useOfficialOpenAIResponses(mod.API, baseURL) {
		return true
	}
	return useOfficialDeepSeekResponses(mod, baseURL)
}

func useOfficialDeepSeekResponses(mod Model, baseURL string) bool {
	return isOfficialDeepSeekResponsesModel(mod.Name) &&
		useOfficialDeepSeekEndpoint(mod.API, baseURL)
}

func isOfficialDeepSeekResponsesModel(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "deepseek-v4-flash", "deepseek-v4-pro":
		return true
	}
	return false
}

func useOfficialDeepSeekEndpoint(api, baseURL string) bool {
	if !strings.EqualFold(strings.TrimSpace(api), "deepseek") {
		return false
	}
	if strings.TrimSpace(baseURL) == "" {
		return true
	}
	u, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(u.Hostname(), "api.deepseek.com")
}

// newStreamClient creates the appropriate stream.Client for the given API
// backend. This consolidates the provider switch that was duplicated in
// startCompletionCmd.
func newStreamClient(api string, accfg anthropic.Config, gccfg google.Config,
	occfg ollama.Config, ccfg openai.Config,
) (stream.Client, error) {
	switch api {
	case "anthropic":
		return anthropic.New(accfg), nil
	case "google":
		return google.New(gccfg), nil
	case "ollama":
		c, err := ollama.New(occfg)
		if err != nil {
			return nil, fmt.Errorf("ollama: %w", err)
		}
		return c, nil
	default:
		if ccfg.UseResponses {
			debug.Printf("OpenAI protocol: responses (profile=%s, store=false)", ccfg.ResponsesProfile)
		} else {
			debug.Printf("OpenAI protocol: chat-completions")
		}
		return openai.New(ccfg), nil
	}
}

func useOfficialOpenAIResponses(api, baseURL string) bool {
	if !strings.EqualFold(strings.TrimSpace(api), "openai") {
		return false
	}
	if strings.TrimSpace(baseURL) == "" {
		return true
	}
	u, err := url.Parse(baseURL)
	return err == nil && strings.EqualFold(u.Hostname(), "api.openai.com")
}

// applyGoogleBaseURLOverride combines a user-supplied Google API URL with
// the model name. The URL is treated as a full streaming endpoint (mirroring
// what google.DefaultConfig builds) and may include the literal token
// "{model}", which is replaced with the path-escaped model name. Users who
// proxy a single Gemini model can supply a URL without a placeholder and
// have it used verbatim.
func applyGoogleBaseURLOverride(base, model string) string {
	if !strings.Contains(base, "{model}") {
		return base
	}
	return strings.ReplaceAll(base, "{model}", url.PathEscape(model))
}
