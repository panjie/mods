package app

import (
	"context"
	"fmt"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

func providerSupportsTools(api string) bool {
	switch api {
	case "cohere", "google":
		return false
	default:
		return true
	}
}

func explicitlyEnabledTools(cfg *Config) bool {
	if cfg.WebSearch ||
		cfg.BuiltinTools.Filesystem == cfgpkg.FilesystemAlways ||
		cfg.BuiltinTools.Shell ||
		cfg.BuiltinTools.SequentialThinking {
		return true
	}
	for name := range cfg.MCPServers {
		if mcpclient.IsEnabled(cfg, name) {
			return true
		}
	}
	return false
}

func (m *Mods) buildToolRegistryForProvider(
	ctx context.Context,
	cfg *Config,
	wscfg websearch.Config,
	prompt string,
	api string,
) (*toolregistry.Registry, error) {
	if providerSupportsTools(api) {
		return BuildRegistry(ctx, cfg, wscfg, prompt)
	}
	if explicitlyEnabledTools(cfg) {
		return nil, modsError{
			Err: fmt.Errorf("%s provider does not support tool execution", api),
			ReasonText: fmt.Sprintf(
				"Tools are not supported for the %s provider. Use OpenAI, Anthropic, Ollama, or an OpenAI-compatible provider for tools.",
				api,
			),
		}
	}
	debug.Printf("Tools skipped: provider %q does not support tool execution", api)
	return toolregistry.NewRegistry(), nil
}
