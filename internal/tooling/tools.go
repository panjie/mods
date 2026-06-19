package tooling

import (
	"context"
	"regexp"
	"runtime"
	"strings"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

var filesystemPathPattern = regexp.MustCompile(`(?i)(^|\s)(\.?/[\w.-]+|[\w.-]+/[\w./-]+|[\w.-]+\.(go|ts|tsx|js|jsx|py|rs|java|c|cc|cpp|h|hpp|md|txt|json|yaml|yml|toml|mod|sum|sh|sql))($|\s|[,.，。:：;；])`)

func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string) (*toolregistry.Registry, error) {
	registry := toolregistry.NewRegistry()

	root := cfg.ResolveWorkspaceRoot()

	if ShouldEnableFilesystemTools(cfg, prompt) {
		if err := toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{
			Root: root,
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
		}); err != nil {
			return nil, err
		}
		if runtime.GOOS == "windows" {
			if err := toolregistry.RegisterPowerShell(registry, toolregistry.ShellConfig{
				Root:           root,
				Timeout:        cfg.BuiltinTools.ShellTimeout,
				MaxOutputChars: cfg.BuiltinTools.ShellMaxOutput,
			}); err != nil {
				return nil, err
			}
		}
	}

	if cfg.BuiltinTools.SequentialThinking {
		if err := toolregistry.RegisterThinking(registry); err != nil {
			return nil, err
		}
	}

	if err := mcpclient.RegisterTools(ctx, cfg, registry); err != nil {
		return nil, err
	}

	return registry, nil
}

func ShouldEnableFilesystemTools(cfg *cfgpkg.Config, prompt string) bool {
	switch cfg.BuiltinTools.Filesystem {
	case cfgpkg.FilesystemAlways:
		return true
	case cfgpkg.FilesystemNever:
		return false
	case "", cfgpkg.FilesystemAuto:
		return PromptLooksFileRelated(prompt)
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
