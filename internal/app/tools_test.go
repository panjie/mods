package app

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/cohere"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
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

func TestToolCapabilities(t *testing.T) {
	cfg := defaultConfig()
	cfg.BuiltinTools.Filesystem = FilesystemAlways
	cfg.BuiltinTools.Shell = true
	cfg.BuiltinTools.SequentialThinking = true
	cfg.WebSearch = true
	registry, err := buildToolRegistry(context.Background(), &cfg, websearch.Config{Provider: "duckduckgo"}, "hello")
	if err != nil {
		t.Fatalf("build registry: %v", err)
	}

	for _, name := range []string{"fs_read_file", "fs_list_dir", "fs_stat", "fs_search", "web_search", "thinking_note"} {
		if !registry.ReadOnly(name) {
			t.Fatalf("expected %s to be read-only", name)
		}
	}
	for _, name := range []string{"fs_write_file", "fs_apply_patch", "shell_run"} {
		if !registry.Mutable(name) {
			t.Fatalf("expected %s to be mutable", name)
		}
	}
	if !registry.ShellExecution("shell_run") {
		t.Fatal("expected shell_run to be shell execution")
	}
}

func TestBuildToolRegistryForUnsupportedProvider(t *testing.T) {
	// helper: build a real stream.Client for the given api name. The
	// Client is never used to issue requests; it only needs to satisfy
	// the stream.Client interface and report the right Capabilities().
	clientFor := func(api string) (stream.Client, error) {
		return newStreamClient(api, anthropic.Config{}, google.Config{}, cohere.Config{}, ollama.Config{}, openai.Config{})
	}

	t.Run("implicit auto filesystem is skipped for unsupported provider", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.BuiltinTools.Filesystem = FilesystemAuto
		mods := &Mods{ctx: context.Background()}
		client, err := clientFor("cohere")
		require.NoError(t, err)
		registry, err := mods.buildToolRegistryForProvider(context.Background(), &cfg, websearch.Config{}, "read README.md", client)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if registry.Len() != 0 {
			t.Fatalf("expected no tools for unsupported provider, got %d", registry.Len())
		}
	})

	t.Run("explicit tools error", func(t *testing.T) {
		cfg := defaultConfig()
		cfg.WebSearch = true
		mods := &Mods{ctx: context.Background()}
		client, err := clientFor("cohere")
		require.NoError(t, err)
		_, err = mods.buildToolRegistryForProvider(context.Background(), &cfg, websearch.Config{Enabled: true}, "hello", client)
		if err == nil {
			t.Fatal("expected explicit tools to fail for unsupported provider")
		}
	})

	t.Run("supported providers keep tools", func(t *testing.T) {
		for _, provider := range []string{"openai", "anthropic", "ollama", "google"} {
			t.Run(provider, func(t *testing.T) {
				cfg := defaultConfig()
				cfg.BuiltinTools.Filesystem = FilesystemAlways
				mods := &Mods{ctx: context.Background()}
				client, err := clientFor(provider)
				require.NoError(t, err)
				registry, err := mods.buildToolRegistryForProvider(context.Background(), &cfg, websearch.Config{}, "hello", client)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if registry.Len() == 0 {
					t.Fatal("expected tools for supported provider")
				}
			})
		}
	})
}
