package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	toolregistry "github.com/charmbracelet/mods/internal/tools"
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

func TestToolCallContextTimeoutPolicy(t *testing.T) {
	cfg := defaultConfig()
	cfg.MCPTimeout = 10 * time.Millisecond
	cfg.BuiltinTools.Shell = true
	cfg.BuiltinTools.SequentialThinking = true
	cfg.WebSearch = false

	registry, err := buildToolRegistry(context.Background(), &cfg, websearch.Config{}, "hello")
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	mods := &Mods{ctx: context.Background()}

	if got := registry.TimeoutPolicy("shell_run"); got != toolregistry.TimeoutPolicySelf {
		t.Fatalf("unexpected shell timeout policy: %q", got)
	}
	ctx, cancel := mods.toolCallContext(registry, "shell_run", &cfg)
	defer cancel()
	if _, ok := ctx.Deadline(); ok {
		t.Fatal("shell_run context should not inherit mcp-timeout deadline")
	}

	if runtime.GOOS == "windows" {
		if got := registry.TimeoutPolicy("powershell_run"); got != toolregistry.TimeoutPolicySelf {
			t.Fatalf("unexpected powershell timeout policy: %q", got)
		}
		ctx, cancel := mods.toolCallContext(registry, "powershell_run", &cfg)
		defer cancel()
		if _, ok := ctx.Deadline(); ok {
			t.Fatal("powershell_run context should not inherit mcp-timeout deadline")
		}
	}

	ctx, cancel = mods.toolCallContext(registry, "thinking_note", &cfg)
	defer cancel()
	if _, ok := ctx.Deadline(); !ok {
		t.Fatal("caller-timed tool context should use mcp-timeout deadline")
	}
}
