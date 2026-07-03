package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
	"github.com/stretchr/testify/require"
)

// swapExecutableDir replaces the executableDir resolver for the duration of
// a test. Passing "" forces the standard XDG/home paths (portable mode can
// never engage); passing a real dir simulates the binary living there.
func swapExecutableDir(dir string) func() {
	save := executableDir
	if dir == "" {
		executableDir = func() string { return "" }
	} else {
		executableDir = func() string { return dir }
	}
	return func() { executableDir = save }
}

func TestPortableConfigPath(t *testing.T) {
	dir := t.TempDir()
	defer swapExecutableDir(dir)()

	// No mods.yml next to the "executable" yet → portable inactive.
	_, ok := portableConfigPath()
	require.False(t, ok)
	require.False(t, portableActive())

	// Create mods.yml next to the "executable" → portable engages.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mods.yml"), []byte(""), 0o600))

	p, ok := portableConfigPath()
	require.True(t, ok)
	require.True(t, portableActive())
	require.Equal(t, filepath.Join(dir, "mods.yml"), p)
}

func TestPortableConfigPathInactiveWhenExeDirUnknown(t *testing.T) {
	defer swapExecutableDir("")()

	_, ok := portableConfigPath()
	require.False(t, ok)
	require.False(t, portableActive())
}

func TestSettingsFilePathPortableWinsOverXDG(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mods.yml"), []byte(""), 0o600))
	// XDG_CONFIG_HOME would normally redirect the standard path, but
	// portable mode must take precedence and ignore it.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	defer swapExecutableDir(dir)()

	p, err := settingsFilePath()
	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "mods.yml"), p)
}

func TestDefaultSessionDirPortableUsesExeDir(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mods.yml"), []byte(""), 0o600))
	// XDG_DATA_HOME must be ignored while portable is active.
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	defer swapExecutableDir(dir)()

	require.Equal(t, filepath.Join(dir, "sessions"), defaultSessionDir())
}

func TestDefaultSessionDirFallsBackToXDG(t *testing.T) {
	dataHome := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataHome)
	defer swapExecutableDir("")()

	require.Equal(t, filepath.Join(xdg.DataHome, "mods", "sessions"), defaultSessionDir())
}

func TestEnsurePortableModePopulatesFields(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "mods.yml"), []byte("default-api: openai\n"), 0o600))
	// Both XDG homes are set to unrelated temp dirs to prove portable wins.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	defer swapExecutableDir(dir)()

	cfg, err := Ensure()

	require.NoError(t, err)
	require.Equal(t, filepath.Join(dir, "mods.yml"), cfg.SettingsPath)
	require.Equal(t, dir, cfg.PortableDir)
	require.Equal(t, filepath.Join(dir, "sessions"), cfg.SessionDir)
	// The sessions directory is created by Ensure.
	require.DirExists(t, filepath.Join(dir, "sessions"))
}
