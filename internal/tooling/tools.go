package tooling

import (
	"context"
	"os"
	"runtime"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/mcpclient"
	"github.com/panjie/mods/internal/self"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
)

func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string) (*toolregistry.Registry, error) {
	registry := toolregistry.NewRegistry()

	workspace := cfg.ResolveWorkspace()
	root := workspace.Canonical

	if ShouldEnableFilesystemTools(cfg, prompt) {
		safeDirs := []string{os.TempDir()}
		if cfg.EvolveAutoImprove {
			safeDirs = nil
		}
		if err := toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{
			Root:     root,
			SafeDirs: safeDirs,
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
	if cfg.Plan {
		return true
	}
	switch cfg.BuiltinTools.Filesystem {
	case cfgpkg.FilesystemAlways:
		return true
	case cfgpkg.FilesystemNever:
		return false
	case "", cfgpkg.FilesystemAuto:
		return self.PromptLooksFileRelated(prompt)
	default:
		return false
	}
}
