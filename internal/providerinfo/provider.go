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
	Profile        string
	Description    string
	DefaultBaseURL string
	APIKeyEnv      string
	APIKeyURL      string
}

var descriptors = map[string]Descriptor{
	"openai": {
		Protocol:       "openai",
		Profile:        "openai",
		Description:    "OpenAI API",
		DefaultBaseURL: "https://api.openai.com/v1",
		APIKeyEnv:      "OPENAI_API_KEY",
		APIKeyURL:      "https://platform.openai.com/account/api-keys",
	},
	"anthropic": {
		Protocol:       "anthropic",
		Profile:        "anthropic",
		Description:    "Anthropic API",
		DefaultBaseURL: "https://api.anthropic.com/v1",
		APIKeyEnv:      "ANTHROPIC_API_KEY",
		APIKeyURL:      "https://console.anthropic.com/settings/keys",
	},
	"google": {
		Protocol:       "google",
		Profile:        "google",
		Description:    "Google AI",
		DefaultBaseURL: "https://generativelanguage.googleapis.com/v1beta/models/{model}:streamGenerateContent?alt=sse",
		APIKeyEnv:      "GOOGLE_API_KEY",
		APIKeyURL:      "https://aistudio.google.com/app/apikey",
	},
	"github-copilot": {
		Protocol:       "github-copilot",
		Profile:        "openai",
		Description:    "GitHub Copilot",
		DefaultBaseURL: "https://api.githubcopilot.com",
	},
	"ollama": {
		Protocol:       "ollama",
		Profile:        "ollama",
		Description:    "Local model runtime (no API key needed)",
		DefaultBaseURL: "http://localhost:11434",
	},
	"azure": {
		Protocol:    "azure",
		Profile:     "openai",
		Description: "Azure OpenAI",
		APIKeyEnv:   "AZURE_OPENAI_KEY",
		APIKeyURL:   "https://aka.ms/oai/access",
	},
	"deepseek": {
		Protocol:       "openai",
		Profile:        "deepseek",
		Description:    "DeepSeek API",
		DefaultBaseURL: "https://api.deepseek.com",
		APIKeyEnv:      "DEEPSEEK_API_KEY",
		APIKeyURL:      "https://platform.deepseek.com/api_keys",
	},
	"glm":        {Protocol: "openai", Profile: "glm", Description: "Zhipu AI"},
	"qwen":       {Protocol: "openai", Profile: "qwen", Description: "Alibaba Cloud"},
	"kimi":       {Protocol: "openai", Profile: "kimi", Description: "Moonshot AI"},
	"minimax":    {Protocol: "openai", Profile: "minimax", Description: "MiniMax API"},
	"openrouter": {Protocol: "openai", Profile: "openai", Description: "Multi-provider API gateway"},
}

// NamedDescriptor associates built-in provider metadata with its config name.
type NamedDescriptor struct {
	Name string
	Descriptor
}

var protocols = []string{"openai", "anthropic", "google", "ollama", "azure", "github-copilot"}
var profiles = []string{"openai", "deepseek", "qwen", "glm", "kimi", "minimax", "anthropic", "google", "ollama"}

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

// Profiles returns every accepted provider-profile value. A profile describes
// a provider's request dialect independently from the transport protocol.
func Profiles() []string {
	return append([]string(nil), profiles...)
}

func KnownProfile(profile string) bool {
	return slices.Contains(profiles, strings.ToLower(strings.TrimSpace(profile)))
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

// Profile resolves a model override first, then a provider override, followed
// by built-in provider metadata. Unknown OpenAI-compatible providers use the
// OpenAI dialect without inheriting behavior from a similarly named model.
func Profile(name, providerProfile, modelProfile string) string {
	if value := strings.ToLower(strings.TrimSpace(modelProfile)); KnownProfile(value) {
		return value
	}
	if value := strings.ToLower(strings.TrimSpace(providerProfile)); KnownProfile(value) {
		return value
	}
	if d, ok := Lookup(name); ok && d.Profile != "" {
		return d.Profile
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

func Auth(nameOrProtocol string) (envName, keyURL string) {
	if d, ok := Lookup(nameOrProtocol); ok && (d.APIKeyEnv != "" || d.APIKeyURL != "") {
		return d.APIKeyEnv, d.APIKeyURL
	}
	if d, ok := Lookup(Protocol("", nameOrProtocol)); ok {
		return d.APIKeyEnv, d.APIKeyURL
	}
	d, _ := Lookup("openai")
	return d.APIKeyEnv, d.APIKeyURL
}
