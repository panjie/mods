package app

import (
	"context"
	"fmt"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

func explicitlyEnabledTools(cfg *Config) bool {
	if cfg.WebSearch ||
		cfg.BuiltinTools.Filesystem == cfgpkg.FilesystemAlways ||
		cfg.BuiltinTools.Shell {
		return true
	}
	if len(cfg.MCPServers) > 0 {
		return true
	}
	return false
}

// buildToolRegistryForProvider decides whether to register native tools
// (filesystem, shell, websearch) and MCP servers,
// based on the provider backend's declared capabilities.
//
// The capability check is delegated to the adapter via
// client.Capabilities().Tools; there is no longer an app-layer string
// switch keyed on the API name, so adding a new provider that supports
// tools only requires implementing Capabilities() correctly on its
// Client. Providers that do not support tools fail closed: if the user
// explicitly enabled any tool, an error is returned; otherwise an empty
// registry is returned so the request proceeds without tool specs.
func (m *Mods) buildToolRegistryForProvider(
	ctx context.Context,
	cfg *Config,
	wscfg websearch.Config,
	prompt string,
	client stream.Client,
) (*toolregistry.Registry, error) {
	if client.Capabilities().Tools {
		handlers := toolregistry.InteractionHandlers{ShellProgress: m.handleShellProgress}
		if m.userInput != nil && m.userInput.available() {
			handlers.UserInput = m.handleUserInput
			handlers.SudoPrompt = m.handleSudoPrompt
		}
		return BuildRegistry(ctx, cfg, wscfg, prompt, m.skillCatalog, handlers)
	}
	if explicitlyEnabledTools(cfg) {
		return nil, modsError{
			Err:        fmt.Errorf("provider does not support tool execution"),
			ReasonText: "Tools are not supported for this provider. Use OpenAI, Anthropic, Ollama, or an OpenAI-compatible provider for tools.",
		}
	}
	debug.Printf("Tools skipped: provider does not support tool execution")
	return toolregistry.NewRegistry(), nil
}
