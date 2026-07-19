package app

import (
	"context"
	"fmt"
	"strings"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/textutil"
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
		m.selfHelpFallback = ""
		handlers := toolregistry.InteractionHandlers{
			ShellProgress: m.handleShellProgress,
			SelfHelp:      m.selfHelpReference,
		}
		if m.userInput != nil && m.userInput.available() {
			handlers.UserInput = m.handleUserInput
			handlers.SudoPrompt = m.handleSudoPrompt
		}
		return BuildRegistry(ctx, cfg, wscfg, prompt, m.skillCatalog, handlers)
	}
	if explicitlyEnabledTools(cfg) {
		m.selfHelpFallback = ""
		return nil, modsError{
			Err:        fmt.Errorf("provider does not support tool execution"),
			ReasonText: "Tools are not supported for this provider. Use OpenAI, Anthropic, Ollama, or an OpenAI-compatible provider for tools.",
		}
	}
	m.selfHelpFallback = formatSelfHelpFallback(m.selfHelpReference, cfg, prompt)
	debug.Printf("Tools skipped: provider does not support tool execution")
	return toolregistry.NewRegistry(), nil
}

const maxToolIntentChars = 8 * 1024

func toolIntentContext(messages []proto.Message) string {
	var recent []string
	used := 0
	for i := len(messages) - 1; i >= 0 && used < maxToolIntentChars; i-- {
		if messages[i].Role != proto.RoleUser || messages[i].Content == "" {
			continue
		}
		content := messages[i].Content
		remaining := maxToolIntentChars - used
		if len(content) > remaining {
			content = textutil.TruncateUTF8Bytes(content, remaining)
		}
		recent = append(recent, content)
		used += len(content)
	}
	var sb strings.Builder
	for i := len(recent) - 1; i >= 0; i-- {
		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}
		sb.WriteString(recent[i])
	}
	return sb.String()
}

func formatSelfHelpFallback(reference selfhelp.Reference, cfg *Config, prompt string) string {
	topic, ok := selfhelp.DetectTopic(prompt)
	if !ok {
		return ""
	}
	content, err := reference.Lookup(topic)
	if err != nil {
		return ""
	}
	path := cfg.SettingsPath
	if path == "" {
		path = "(unavailable)"
	}
	portable := "inactive"
	if cfg.PortableDir != "" {
		portable = "active"
	}
	mode := string(cfg.BuiltinTools.Filesystem)
	if mode == "" {
		mode = string(cfgpkg.FilesystemAuto)
	}
	return fmt.Sprintf(
		"Mods self-help fallback (this provider cannot call tools).\n"+
			"Active config path: %s\nPortable mode: %s\nFilesystem tools: %s\n"+
			"Direct config editing is unavailable with this provider. Config changes take effect on the next mods invocation.\n\n%s",
		path, portable, mode, content,
	)
}

func (m *Mods) injectSelfHelpFallback() {
	content := m.selfHelpFallback
	m.selfHelpFallback = ""
	if content == "" {
		return
	}
	insertAt := 0
	for insertAt < len(m.messages) && m.messages[insertAt].Role == proto.RoleSystem {
		insertAt++
	}
	msg := proto.Message{Role: proto.RoleSystem, Content: content}
	m.messages = append(m.messages, proto.Message{})
	copy(m.messages[insertAt+1:], m.messages[insertAt:])
	m.messages[insertAt] = msg
}
