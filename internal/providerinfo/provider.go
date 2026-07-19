// Package providerinfo owns provider metadata that is shared by the runtime
// and configuration UI. Adapter-specific request construction remains in the
// provider packages.
package providerinfo

import (
	"slices"
	"strings"
)

type Descriptor struct {
	Protocol       string
	Description    string
	DefaultBaseURL string
	APIKeyEnv      string
	APIKeyURL      string
}

var descriptors = map[string]Descriptor{
	"openai": {
		Protocol:       "openai",
		Description:    "GPT-5.x, o3, o4-mini",
		DefaultBaseURL: "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		APIKeyURL:      "https://platform.openai.com/account/api-keys",
	},
	"anthropic": {
		Protocol:       "anthropic",
		Description:    "Claude Opus/Sonnet/Haiku",
		DefaultBaseURL: "https://api.anthropic.com/v1",
		APIKeyEnv:      "ANTHROPIC_API_KEY",
		APIKeyURL:      "https://console.anthropic.com/settings/keys",
	},
	"google": {
		Protocol:       "google",
		Description:    "Gemini Pro/Flash",
		DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse",
		APIKeyEnv:      "GOOGLE_API_KEY",
		APIKeyURL:      "https://aistudio.google.com/app/apikey",
	},
	"ollama": {
		Protocol:       "ollama",
		Description:    "Local models (no API key needed)",
		DefaultBaseURL: "http://localhost:11434",
	},
	"azure": {
		Protocol:    "azure",
		Description: "Azure OpenAI",
		APIKeyEnv:   "AZURE_OPENAI_KEY",
		APIKeyURL:   "https://aka.ms/oai/access",
	},
	"azure-ad": {
		Protocol:  "azure-ad",
		APIKeyEnv: "AZURE_OPENAI_KEY",
		APIKeyURL: "https://aka.ms/oai/access",
	},
	"deepseek":   {Protocol: "openai", Description: "DeepSeek V4 (reasoning)"},
	"glm":        {Protocol: "openai", Description: "GLM-5.2 (Zhipu)"},
	"qwen":       {Protocol: "openai", Description: "Qwen (Alibaba)"},
	"kimi":       {Protocol: "openai", Description: "Kimi K2 (Moonshot)"},
	"minimax":    {Protocol: "openai", Description: "MiniMax M3"},
	"openrouter": {Protocol: "openai", Description: "Multi-provider aggregator"},
}

// NamedDescriptor associates built-in provider metadata with its config name.
type NamedDescriptor struct {
	Name string
	Descriptor
}

var protocols = []string{"openai", "anthropic", "google", "ollama", "azure", "azure-ad"}

// Descriptors returns built-in provider metadata in stable name order.
func Descriptors() []NamedDescriptor {
	result := make([]NamedDescriptor, 0, len(descriptors))
	for name, descriptor := range descriptors {
		result = append(result, NamedDescriptor{Name: name, Descriptor: descriptor})
	}
	slices.SortFunc(result, func(a, b NamedDescriptor) int {
		return strings.Compare(a.Name, b.Name)
	})
	return result
}

// Protocols returns every accepted api-type value.
func Protocols() []string {
	return append([]string(nil), protocols...)
}

func Lookup(name string) (Descriptor, bool) {
	d, ok := descriptors[strings.ToLower(strings.TrimSpace(name))]
	return d, ok
}

func KnownProtocol(protocol string) bool {
	return slices.Contains(protocols, strings.ToLower(strings.TrimSpace(protocol)))
}

// Protocol resolves an explicit api-type first, then built-in name metadata,
// and finally the OpenAI-compatible default.
func Protocol(name, apiType string) string {
	if value := strings.ToLower(strings.TrimSpace(apiType)); KnownProtocol(value) {
		return value
	}
	if d, ok := Lookup(name); ok {
		return d.Protocol
	}
	return "openai"
}

func IsOpenAICompatible(protocol string) bool {
	return Protocol("", protocol) == "openai"
}

func DefaultBaseURL(name string) string {
	if d, ok := Lookup(name); ok && d.DefaultBaseURL != "" {
		return d.DefaultBaseURL
	}
	return "https://your-server.com/v1"
}

func Auth(protocol string) (envName, keyURL string) {
	if d, ok := Lookup(Protocol("", protocol)); ok {
		return d.APIKeyEnv, d.APIKeyURL
	}
	d, _ := Lookup("openai")
	return d.APIKeyEnv, d.APIKeyURL
}
