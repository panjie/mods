package main

import (
	"context"
	"os"

	toolregistry "github.com/charmbracelet/mods/internal/tools"
	"github.com/charmbracelet/mods/internal/websearch"
)

func buildToolRegistry(ctx context.Context, cfg *Config, wscfg websearch.Config) (*toolregistry.Registry, error) {
	registry := toolregistry.NewRegistry()

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	if cfg.BuiltinTools.Filesystem {
		if err := toolregistry.RegisterFilesystem(registry, toolregistry.FilesystemConfig{
			Root: cwd,
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
			Root:           cwd,
			Timeout:        cfg.BuiltinTools.ShellTimeout,
			MaxOutputChars: cfg.BuiltinTools.ShellMaxOutput,
		}); err != nil {
			return nil, err
		}
	}

	if cfg.BuiltinTools.SequentialThinking {
		if err := toolregistry.RegisterThinking(registry); err != nil {
			return nil, err
		}
	}

	if err := registerMCPTools(ctx, registry); err != nil {
		return nil, err
	}

	return registry, nil
}
