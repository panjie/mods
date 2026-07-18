package tooling

import (
	"context"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/panjie/mods/internal/approval"
	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/skills"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

var filesystemPathPattern = regexp.MustCompile(`(?i)(^|\s)(\.?/[\w.-]+|[\w.-]+/[\w./-]+|[\w.-]+\.(go|ts|tsx|js|jsx|py|rs|java|c|cc|cpp|h|hpp|md|txt|json|yaml|yml|toml|mod|sum|sh|sql))($|\s|[,.，。:：;；])`)

func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string, skillCatalog []skills.Skill, interaction ...toolregistry.InteractionHandlers) (*toolregistry.Registry, error) {
	registry := toolregistry.NewRegistry()
	var handlers toolregistry.InteractionHandlers
	if len(interaction) > 0 {
		handlers = interaction[0]
	}

	workspace := cfg.ResolveWorkspace()
	root := workspace.Canonical

	if err := toolregistry.RegisterModsHelp(registry, toolregistry.ModsHelpConfig{
		SettingsPath:   cfg.SettingsPath,
		Portable:       cfg.PortableDir != "",
		FilesystemMode: string(cfg.BuiltinTools.Filesystem),
	}); err != nil {
		return nil, err
	}

	if ShouldEnableFilesystemTools(cfg, prompt) {
		if err := toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{
			Root:     root,
			SafeDirs: approval.SafeDirs(),
		}); err != nil {
			return nil, err
		}
	}

	if cfg.WebSearch {
		if err := toolregistry.RegisterWebSearch(registry, wscfg); err != nil {
			return nil, err
		}
	}

	if cfg.BuiltinTools.Shell {
		if err := toolregistry.RegisterShell(registry, toolregistry.ShellConfig{
			Root:           root,
			Timeout:        cfg.BuiltinTools.ShellTimeout,
			MaxOutputChars: cfg.BuiltinTools.ShellMaxOutput,
			SudoPrompt:     handlers.SudoPrompt,
			Progress:       handlers.ShellProgress,
		}); err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" {
			if err := toolregistry.RegisterPowerShell(registry, toolregistry.ShellConfig{
				Root:           root,
				Timeout:        cfg.BuiltinTools.ShellTimeout,
				MaxOutputChars: cfg.BuiltinTools.ShellMaxOutput,
				Progress:       handlers.ShellProgress,
			}); err != nil {
				return nil, err
			}
		}
	}

	if err := mcpclient.RegisterTools(ctx, cfg, registry); err != nil {
		return nil, err
	}

	if len(skillCatalog) > 0 {
		if err := toolregistry.RegisterSkill(registry, skillCatalog); err != nil {
			return nil, err
		}
	}

	if handlers.UserInput != nil {
		if err := toolregistry.RegisterUserInput(registry, handlers.UserInput); err != nil {
			return nil, err
		}
	}

	return registry, nil
}

func ShouldEnableFilesystemTools(cfg *cfgpkg.Config, prompt string) bool {
	if cfg.Plan {
		return true
	}
	switch cfg.BuiltinTools.Filesystem {
	case cfgpkg.FilesystemAlways:
		return true
	case cfgpkg.FilesystemNever:
		return false
	case "", cfgpkg.FilesystemAuto:
		if selfhelp.IsConfigHelpOnly(prompt) {
			return false
		}
		return PromptLooksFileRelated(prompt) ||
			selfhelp.IsConfigMutation(prompt) ||
			selfhelp.IsConfigInspection(prompt)
	default:
		return false
	}
}

func PromptLooksFileRelated(prompt string) bool {
	p := strings.ToLower(prompt)
	keywords := []string{
		"file", "files", "directory", "folder", "repo", "repository",
		"codebase", "source", "write", "edit", "modify", "patch",
		"grep", "rg",
		"文件", "目录", "代码", "仓库", "项目",
		"修改", "编辑", "修复",
	}
	for _, keyword := range keywords {
		if strings.Contains(p, keyword) {
			return true
		}
	}
	return filesystemPathPattern.MatchString(prompt)
}

// BuiltinToolInfo describes one built-in tool for listing/discovery.
type BuiltinToolInfo struct {
	Name        string
	Description string
	Kind        toolregistry.ToolKind
	ReadOnly    bool
	Mutable     bool
	Shell       bool
	Interactive bool
}

// BuiltinSpecs enumerates every built-in tool mods can provide, independent of
// runtime enablement (which depends on prompt and config). It powers
// --list-tools' built-in catalogue. Tools are registered into a throwaway
// registry (their Call closures are never invoked) purely to harvest specs and
// capabilities, so this works offline and without API keys.
func BuiltinSpecs() ([]BuiltinToolInfo, error) {
	root, err := os.MkdirTemp("", "mods-list-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(root)
	registry := toolregistry.NewRegistry()
	// Best-effort registration; listing must not fail if one tool errors.
	_ = toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{Root: root})
	_ = toolregistry.RegisterShell(registry, toolregistry.ShellConfig{Root: root})
	if runtime.GOOS == "windows" {
		_ = toolregistry.RegisterPowerShell(registry, toolregistry.ShellConfig{Root: root})
	}
	_ = toolregistry.RegisterWebSearch(registry, websearch.Config{})
	_ = toolregistry.RegisterModsHelp(registry, toolregistry.ModsHelpConfig{})
	_ = toolregistry.RegisterSkill(registry, nil)
	_ = toolregistry.RegisterUserInput(registry, nil)

	infos := make([]BuiltinToolInfo, 0, registry.Len())
	for _, spec := range registry.Specs() {
		tool, ok := registry.Tool(spec.Name)
		if !ok {
			continue
		}
		infos = append(infos, BuiltinToolInfo{
			Name:        spec.Name,
			Description: spec.Description,
			Kind:        tool.Kind,
			ReadOnly:    tool.Capabilities.ReadOnly,
			Mutable:     tool.Capabilities.Mutable,
			Shell:       tool.Capabilities.ShellExecution,
			Interactive: tool.Capabilities.Interactive,
		})
	}
	return infos, nil
}
