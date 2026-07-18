package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/providerinfo"
	"github.com/panjie/mods/internal/stream"
)

type providerConfigs struct {
	Anthropic anthropic.Config
	Google    google.Config
	Ollama    ollama.Config
	OpenAI    openai.Config
}

func (m *Mods) buildProviderConfigs(mod Model, api API) (providerConfigs, error) {
	var cfgs providerConfigs
	keyEnv, keyURL := providerinfo.Auth(mod.API)
	switch mod.API {
	case "ollama":
		cfgs.Ollama = ollama.DefaultConfig()
		if api.BaseURL != "" {
			cfgs.Ollama.BaseURL = api.BaseURL
		}
	case "anthropic":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Anthropic authentication failed"}
		}
		cfgs.Anthropic = anthropic.DefaultConfig(key)
		if api.BaseURL != "" {
			cfgs.Anthropic.BaseURL = api.BaseURL
		}
	case "google":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Google authentication failed"}
		}
		cfgs.Google = google.DefaultConfig(mod.Name, key)
		cfgs.Google.ThinkingBudget = mod.ThinkingBudget
		if api.BaseURL != "" {
			cfgs.Google.BaseURL = applyGoogleBaseURLOverride(api.BaseURL, mod.Name)
		}
	case "azure", "azure-ad":
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "Azure authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:     key,
			BaseURL:       api.BaseURL,
			ExtraParams:   mod.ExtraParams,
			ThoughtFields: mod.ThinkFields,
			ThinkTag:      mod.ThinkTag,
		}
		if mod.API == "azure-ad" {
			cfgs.OpenAI.APIType = "azure-ad"
		} else {
			cfgs.OpenAI.APIType = "azure"
		}
	default:
		key, err := m.ensureKey(api, keyEnv, keyURL)
		if err != nil {
			return cfgs, modsError{Err: err, ReasonText: "OpenAI authentication failed"}
		}
		cfgs.OpenAI = openai.Config{
			AuthToken:     key,
			BaseURL:       api.BaseURL,
			ExtraParams:   mod.ExtraParams,
			ThoughtFields: mod.ThinkFields,
			ThinkTag:      mod.ThinkTag,
		}
	}
	return cfgs, nil
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
		return openai.New(ccfg), nil
	}
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
