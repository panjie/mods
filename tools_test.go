package main

import (
	"context"
	"runtime"
	"testing"

	"github.com/charmbracelet/mods/internal/websearch"
)

func TestShouldEnableFilesystemTools(t *testing.T) {
	t.Run("auto skips general ollama question", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemAuto
		if shouldEnableFilesystemTools(&cfg, "mods 如何更方便的测试我用ollama安装的大模型的速度") {
			t.Fatal("expected filesystem tools to stay disabled for a general model speed question")
		}
	})

	t.Run("auto enables file-related chinese prompt", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemAuto
		if !shouldEnableFilesystemTools(&cfg, "搜索代码里 toolCall 的实现") {
			t.Fatal("expected filesystem tools for code search prompt")
		}
	})

	t.Run("auto enables path-like prompt", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemAuto
		if !shouldEnableFilesystemTools(&cfg, "总结 README.md 的用法") {
			t.Fatal("expected filesystem tools for path-like prompt")
		}
	})

	t.Run("explicit true overrides prompt", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemAlways
		if !shouldEnableFilesystemTools(&cfg, "hello") {
			t.Fatal("expected filesystem tools when explicitly enabled")
		}
	})

	t.Run("explicit false overrides prompt", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemNever
		if shouldEnableFilesystemTools(&cfg, "读取 README.md") {
			t.Fatal("expected filesystem tools disabled when explicitly disabled")
		}
	})
}

func TestBuildToolRegistryPowerShellRun(t *testing.T) {
	cfg := defaultConfig()
	cfg.BuiltinTools.Shell = true
	cfg.WebSearch = false
	registry, err := buildToolRegistry(context.Background(), &cfg, websearch.Config{}, "hello")
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	found := false
	for _, spec := range registry.Specs() {
		if spec.Name == "powershell_run" {
			found = true
			break
		}
	}
	if runtime.GOOS == "windows" && !found {
		t.Fatal("expected powershell_run on Windows")
	}
	if runtime.GOOS != "windows" && found {
		t.Fatal("did not expect powershell_run outside Windows")
	}
}
