package app

import (
	"fmt"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/cohere"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/stream"
)

type providerConfigs struct {
	Anthropic anthropic.Config
	Google    google.Config
	Cohere    cohere.Config
	Ollama    ollama.Config
	OpenAI    openai.Config
}

func (m *Mods) buildProviderConfigs(mod Model, api API) (providerConfigs, error) {
	var cfgs providerConfigs
	switch mod.API {
	case "ollama":
		cfgs.Ollama = ollama.DefaultConfig()
		if api.BaseURL != "" {
			cfgs.Ollama.BaseURL = api.BaseURL
		}
	case "anthropic":
		key, err := m.ensureKey(api, "ANTHROPIC_API_KEY", "https://console.anthropic.com/settings/keys")
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Anthropic authentication failed"}
		}
		cfgs.Anthropic = anthropic.DefaultConfig(key)
		if api.BaseURL != "" {
			cfgs.Anthropic.BaseURL = api.BaseURL
		}
	case "google":
		key, err := m.ensureKey(api, "GOOGLE_API_KEY", "https://aistudio.google.com/app/apikey")
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Google authentication failed"}
		}
		cfgs.Google = google.DefaultConfig(mod.Name, key)
		cfgs.Google.ThinkingBudget = mod.ThinkingBudget
	case "cohere":
		key, err := m.ensureKey(api, "COHERE_API_KEY", "https://dashboard.cohere.com/api-keys")
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Cohere authentication failed"}
		}
		cfgs.Cohere = cohere.DefaultConfig(key)
		if api.BaseURL != "" {
			cfgs.Cohere.BaseURL = api.BaseURL
		}
	case "azure", "azure-ad":
		key, err := m.ensureKey(api, "AZURE_OPENAI_KEY", "https://aka.ms/oai/access")
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Azure authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:   key,
			BaseURL:     api.BaseURL,
			ExtraParams: mod.ExtraParams,
		}
		if mod.API == "azure-ad" {
			cfgs.OpenAI.APIType = "azure-ad"
		} else {
			cfgs.OpenAI.APIType = "azure"
		}
	default:
		key, err := m.ensureKey(api, "OPENAI_API_KEY", "https://platform.openai.com/account/api-keys")
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "OpenAI authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:   key,
			BaseURL:     api.BaseURL,
			ExtraParams: mod.ExtraParams,
		}
	}
	return cfgs, nil
}

// newStreamClient creates the appropriate stream.Client for the given API
// backend. This consolidates the provider switch that was duplicated in
// startCompletionCmd and judgeTaskComplexity.
func newStreamClient(api string, accfg anthropic.Config, gccfg google.Config,
	cccfg cohere.Config, occfg ollama.Config, ccfg openai.Config,
) (stream.Client, error) {
	switch api {
	case "anthropic":
		return anthropic.New(accfg), nil
	case "google":
		return google.New(gccfg), nil
	case "cohere":
		return cohere.New(cccfg), nil
	case "ollama":
		c, err := ollama.New(occfg)
		if err != nil {
			return nil, fmt.Errorf("ollama: %w", err)
		}
		return c, nil
	default:
		return openai.New(ccfg), nil
	}
}
