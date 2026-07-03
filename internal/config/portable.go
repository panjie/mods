package config

import (
	"os"
	"path/filepath"
)

// portableDir returns the directory containing the running executable,
// resolving symlinks. It returns "" when the directory cannot be determined
// (for example under `go run`, where os.Executable points at a transient
// build cache binary).
func portableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return ""
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	return filepath.Dir(exe)
}

// executableDir is swappable so tests can fake the executable's location.
// os.Executable always reports the compiled test binary, which would
// otherwise make portable-mode detection depend on where that binary
// happens to live. Tests set this to a temp dir (to simulate portable) or
// to a function returning "" (to force the standard XDG paths).
var executableDir = portableDir

// ExeDir returns the resolved directory of the running executable, or "" if
// it cannot be determined. Callers in other packages (e.g. the config
// wizard) use it to compute portable-mode paths and labels.
func ExeDir() string {
	return executableDir()
}

// portableConfigPath reports the config file location to use in portable
// mode: mods.yml placed next to the running executable. It returns
// ("", false) when the executable directory is unknown or no mods.yml
// exists there, so settingsFilePath can fall back to the standard
// XDG/home resolution.
func portableConfigPath() (string, bool) {
	dir := executableDir()
	if dir == "" {
		return "", false
	}
	p := filepath.Join(dir, "mods.yml")
	if _, err := os.Stat(p); err != nil {
		return "", false
	}
	return p, true
}

// portableActive reports whether portable mode is currently engaged, i.e.
// mods.yml exists next to the running executable. When true, config and
// session paths resolve relative to the executable directory and ignore
// XDG_CONFIG_HOME / XDG_DATA_HOME.
func portableActive() bool {
	_, ok := portableConfigPath()
	return ok
}
