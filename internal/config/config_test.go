package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/caarlos0/env/v9"
	"github.com/panjie/mods/internal/prompts"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestConfig(t *testing.T) {
	t.Run("old format text", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("format-text: as markdown"), &cfg))
		require.Equal(t, FormatText(map[string]string{
			"markdown": "as markdown",
		}), cfg.FormatText)
	})
	t.Run("new format text", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("format-text:\n  markdown: as markdown\n  json: as json"), &cfg))
		require.Equal(t, FormatText(map[string]string{
			"markdown": "as markdown",
			"json":     "as json",
		}), cfg.FormatText)
	})
}

func TestPromptConfig(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(`prompts:
  identity: custom identity
  tool-selection: custom tools
  plan: custom plan
  shell-classifier: custom shell
`), &cfg))

	require.Equal(t, "custom identity", cfg.Prompts.Identity)
	require.Equal(t, "custom tools", cfg.Prompts.ToolSelection)
	require.Equal(t, "custom plan", cfg.Prompts.Plan)
	require.Equal(t, "custom shell", cfg.Prompts.ShellClassifier)
	require.Equal(t, "custom identity", cfg.Prompts.Value(prompts.KeyIdentity))
	require.Equal(t, "custom shell", cfg.Prompts.Value(prompts.KeyShellClassifier))
}

func TestValidateReasoningMode(t *testing.T) {
	cfg := &Config{}
	cfg.Reasoning = ""
	validateReasoningMode(cfg)
	require.Equal(t, ReasoningMode(""), cfg.Reasoning) // empty is valid, left alone

	cfg = &Config{}
	cfg.Reasoning = ReasoningOff
	validateReasoningMode(cfg)
	require.Equal(t, ReasoningOff, cfg.Reasoning)

	cfg = &Config{}
	cfg.Reasoning = ReasoningOn
	validateReasoningMode(cfg)
	require.Equal(t, ReasoningOn, cfg.Reasoning)

	cfg = &Config{}
	cfg.Reasoning = "auto"
	validateReasoningMode(cfg)
	require.Equal(t, ReasoningOff, cfg.Reasoning) // invalid resets to default
}

func TestValidateReviewMode(t *testing.T) {
	cfg := &Config{}
	cfg.ReviewMode = ""
	validateReviewMode(cfg)
	require.Equal(t, ReviewMode(""), cfg.ReviewMode) // empty is valid, left alone

	cfg = &Config{}
	cfg.ReviewMode = ReviewAuto
	validateReviewMode(cfg)
	require.Equal(t, ReviewAuto, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = ReviewAlways
	validateReviewMode(cfg)
	require.Equal(t, ReviewAlways, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = ReviewNever
	validateReviewMode(cfg)
	require.Equal(t, ReviewNever, cfg.ReviewMode)

	cfg = &Config{}
	cfg.ReviewMode = "mutable"
	validateReviewMode(cfg)
	require.Equal(t, ReviewAuto, cfg.ReviewMode) // invalid resets to default
}

func TestDefaultPromptText(t *testing.T) {
	cfg := defaultConfig()

	require.Contains(t, MinimalSystemPrompt, "Unless the user explicitly requests otherwise")
	require.Contains(t, MinimalSystemPrompt, "output only the final answer")

	require.Equal(t,
		"Format the response as Markdown. Do not wrap the whole response in a code fence unless the user explicitly requests it.",
		cfg.FormatText["markdown"],
	)
	require.Equal(t,
		"Return valid JSON only. Do not include Markdown fences, prose, or explanations unless the user explicitly requests them.",
		cfg.FormatText["json"],
	)
}

func TestToolSelectionRulesArePrioritized(t *testing.T) {
	require.Contains(t, ToolSelectionRules, "Priority order:")
	require.Contains(t, ToolSelectionRules, "fs_* tools may also access files outside the workspace")
	require.Contains(t, ToolSelectionRules, "such access triggers an approval prompt")
	require.Contains(t, ToolSelectionRules, "Use shell tools for repository-wide inspection")
	require.Contains(t, ToolSelectionRules, "rg --files")
	require.Contains(t, ToolSelectionRules, "powershell_run")
	require.Contains(t, ToolSelectionRules, "go test ./...")
}

func TestWorkspaceHelpUsesWorkspaceTerminology(t *testing.T) {
	require.Contains(t, Help["workspace"], "Set the workspace")
}

func TestDefaultConfigDisplay(t *testing.T) {
	cfg := defaultConfig()
	require.Equal(t, "Generating", cfg.StatusText)

	var fromYAML Config
	fromYAML.PersistentConfig = defaultConfig().PersistentConfig
	require.NoError(t, yaml.Unmarshal([]byte("minimal: true"), &fromYAML))
	require.Equal(t, "Generating", fromYAML.StatusText)
}

func TestMinimalConfig(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("minimal: true"), &cfg))
		require.True(t, cfg.Minimal)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_MINIMAL", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.Minimal)
	})
}

func TestSessionSaveConfig(t *testing.T) {
	t.Run("default is false", func(t *testing.T) {
		require.False(t, Default().NoSave)
	})

	t.Run("no-save is not settable via env or yaml", func(t *testing.T) {
		t.Setenv("MODS_NO_SAVE", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.False(t, cfg.NoSave)

		require.NoError(t, yaml.Unmarshal([]byte("no-save: true"), &cfg))
		require.False(t, cfg.NoSave)
	})
}

func TestHideToolStatusConfig(t *testing.T) {
	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("hide-tool-status: true"), &cfg))
		require.True(t, cfg.HideToolStatus)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_HIDE_TOOL_STATUS", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.HideToolStatus)
	})
}

func TestConfigTemplateIncludesHideToolStatus(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(content), "hide-tool-status: false"))
}

func TestConfigTemplateIncludesPrompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "prompts:")
	require.Contains(t, string(content), `identity: ""`)
	require.Contains(t, string(content), `shell-classifier: ""`)
}

func TestShowToolResultsConfig(t *testing.T) {
	t.Run("default is false", func(t *testing.T) {
		require.False(t, Default().ShowToolResults)
	})

	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("show-tool-results: true"), &cfg))
		require.True(t, cfg.ShowToolResults)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_SHOW_TOOL_RESULTS", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.ShowToolResults)
	})
}

func TestConfigTemplateIncludesShowToolResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "show-tool-results: false")
}

func TestAPITypeYAML(t *testing.T) {
	t.Run("decodes api-type on a custom provider", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte(`apis:
  acme-claude:
    api-type: anthropic
    base-url: https://acme.example.com/v1
    api-key-env: ACME_API_KEY
    models:
      claude-sonnet-4:
        aliases: ["acme-sonnet"]
`), &cfg))
		require.Len(t, cfg.APIs, 1)
		require.Equal(t, "acme-claude", cfg.APIs[0].Name)
		require.Equal(t, "anthropic", cfg.APIs[0].APIType)
		require.Equal(t, "https://acme.example.com/v1", cfg.APIs[0].BaseURL)
	})

	t.Run("api-type defaults to empty", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte(`apis:
  custom:
    base-url: https://example.com/v1
    models:
      m: {}
`), &cfg))
		require.Empty(t, cfg.APIs[0].APIType)
	})
}

func TestConfigTemplateDocumentsAPIType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(content), "api-type: anthropic")
}

func TestFilesystemModeYAML(t *testing.T) {
	t.Run("string auto", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: auto"), &cfg))
		require.Equal(t, FilesystemAuto, cfg.BuiltinTools.Filesystem)
	})

	t.Run("legacy bool true", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: true"), &cfg))
		require.Equal(t, FilesystemAlways, cfg.BuiltinTools.Filesystem)
	})

	t.Run("legacy bool false", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("builtin-tools:\n  filesystem: false"), &cfg))
		require.Equal(t, FilesystemNever, cfg.BuiltinTools.Filesystem)
	})
}

func TestSettingsFilePathDarwin(t *testing.T) {
	defer swapExecutableDir("")()
	if runtime.GOOS != "darwin" {
		t.Skip("darwin-specific default config path")
	}

	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")

	path, err := settingsFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(home, ".config", "mods", "mods.yml"), path)

	xdgConfigHome := filepath.Join(t.TempDir(), "xdg-config")
	t.Setenv("XDG_CONFIG_HOME", xdgConfigHome)

	path, err = settingsFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(xdgConfigHome, "mods", "mods.yml"), path)
}

func TestEnsureReportsSettingsExistedFalseWhenCreatingDefault(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	cfg, err := Ensure()

	require.NoError(t, err)
	require.False(t, cfg.SettingsExisted)
	require.FileExists(t, cfg.SettingsPath)
}

func TestEnsureReportsSettingsExistedTrueWhenFileAlreadyExists(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)

	first, err := Ensure()
	require.NoError(t, err)
	require.False(t, first.SettingsExisted)

	second, err := Ensure()

	require.NoError(t, err)
	require.True(t, second.SettingsExisted)
	require.Equal(t, first.SettingsPath, second.SettingsPath)
}

func TestEnsureResetsInvalidReviewMode(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	path := filepath.Join(configHome, "mods", "mods.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("review-mode: mutable\n"), 0o600))

	cfg, err := Ensure()

	require.NoError(t, err)
	require.Equal(t, ReviewAuto, cfg.ReviewMode)
}

// TestEnsureReturnsCompleteConfigOnError pins the invariant that every
// error path from Ensure() returns a Config the caller can still use:
// SettingsPath is filled in as soon as it is known, SessionDir always has
// the default value, and the rest matches Default(). Callers of
// --settings / --config keep the returned value after a partial failure,
// so a half-initialised Config would surface as zero-valued fields
// downstream.
func TestEnsureReturnsCompleteConfigOnError(t *testing.T) {
	configHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", t.TempDir())

	// Write a syntactically invalid YAML so the yaml.Unmarshal branch in
	// Ensure() takes its error return, exercising one of the middle
	// failure paths.
	path := filepath.Join(configHome, "mods", "mods.yml")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte("not: [a: valid: yaml"), 0o600))

	cfg, err := Ensure()
	require.Error(t, err, "Ensure must surface the parse error")

	// Even on failure, callers must observe a usable Config.
	require.NotEmpty(t, cfg.SettingsPath,
		"SettingsPath must be set so --settings / --config can locate the file")
	require.NotEmpty(t, cfg.SessionDir,
		"SessionDir must have its default so the session layer never sees an empty path")
	require.Equal(t, Default().ReviewMode, cfg.ReviewMode,
		"non-failure fields must keep their Default() value")
	require.Equal(t, Default().WordWrap, cfg.WordWrap)
	require.Equal(t, Default().Format, cfg.Format)
	require.Equal(t, Default().MCPTimeout, cfg.MCPTimeout)
}
func TestCreateConfigFileRefusesToOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	err := createConfigFile(path)
	require.Error(t, err, "createConfigFile must not silently overwrite an existing file")
}

// TestCreateConfigFilePermissionsOnUnix asserts the file is created with
// 0o600 in a single syscall, eliminating the post-create chmod race
// window that previously exposed the API-key template to other users.
// Permission bits on Windows do not follow POSIX conventions, so the test
// is unix-only.
func TestCreateConfigFilePermissionsOnUnix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits do not follow POSIX conventions on Windows")
	}
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}
