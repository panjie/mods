package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/caarlos0/env/v9"
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
	require.Contains(t, ToolSelectionRules, "Use fs_* tools only for files inside workspace_root")
	require.Contains(t, ToolSelectionRules, "Use platform-appropriate shell tools for paths outside workspace_root")
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

func TestHideToolResultsConfig(t *testing.T) {
	t.Run("default is true", func(t *testing.T) {
		require.True(t, Default().HideToolResults)
	})

	t.Run("yaml", func(t *testing.T) {
		var cfg Config
		require.NoError(t, yaml.Unmarshal([]byte("hide-tool-results: true"), &cfg))
		require.True(t, cfg.HideToolResults)
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("MODS_HIDE_TOOL_RESULTS", "true")
		var cfg Config
		require.NoError(t, env.ParseWithOptions(&cfg, env.Options{Prefix: "MODS_"}))
		require.True(t, cfg.HideToolResults)
	})
}

func TestConfigTemplateIncludesHideToolResults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mods.yml")
	require.NoError(t, createConfigFile(path))

	content, err := os.ReadFile(path)
	require.NoError(t, err)
	require.True(t, strings.Contains(string(content), "hide-tool-results: true"))
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

// TestEnsureReturnsCompleteConfigOnError pins the invariant that every
// error path from Ensure() returns a Config the caller can still use:
// SettingsPath is filled in as soon as it is known, CachePath always has
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
	require.NotEmpty(t, cfg.CachePath,
		"CachePath must have its default so the cache layer never sees an empty path")
	require.Equal(t, Default().ReviewMode, cfg.ReviewMode,
		"non-failure fields must keep their Default() value")
	require.Equal(t, Default().WordWrap, cfg.WordWrap)
	require.Equal(t, Default().FormatAs, cfg.FormatAs)
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
