package main

import (
	"path/filepath"
	"runtime"
	"testing"

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
