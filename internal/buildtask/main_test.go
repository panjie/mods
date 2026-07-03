package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestInstallDir(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}

		want := filepath.Join("/usr/local", "bin")
		if runtime.GOOS == "windows" {
			want = filepath.Join(home, ".local", "bin")
		}
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("XDG_DATA_HOME alone does not trigger local bin", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}

		want := filepath.Join("/usr/local", "bin")
		if runtime.GOOS == "windows" {
			want = filepath.Join(home, ".local", "bin")
		}
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("XDG_BIN_HOME wins over local bin", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		want := filepath.Join(home, "custom", "bin")
		t.Setenv("XDG_BIN_HOME", want)

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("legacy XDG switch enables local bin", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		t.Setenv("XDG", "1")

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}

		want := filepath.Join(home, ".local", "bin")
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("PREFIX wins over XDG_BIN_HOME", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		t.Setenv("XDG_BIN_HOME", filepath.Join(home, "xdg-bin", "bin"))
		prefix := filepath.Join(home, "prefix")
		t.Setenv("PREFIX", prefix)

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}

		want := filepath.Join(prefix, "bin")
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("BINDIR wins over PREFIX and XDG_BIN_HOME", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		want := filepath.Join(home, "bin")
		t.Setenv("BINDIR", want)
		t.Setenv("PREFIX", filepath.Join(home, "prefix"))
		t.Setenv("XDG_BIN_HOME", filepath.Join(home, "xdg-bin", "bin"))

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})
}

func TestHasXDGEnv(t *testing.T) {
	t.Run("XDG=1 triggers", func(t *testing.T) {
		clearXDGEnv(t)
		t.Setenv("XDG", "1")
		if !hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return true when XDG=1")
		}
	})

	t.Run("XDG_BIN_HOME triggers", func(t *testing.T) {
		clearXDGEnv(t)
		t.Setenv("XDG_BIN_HOME", "/custom/bin")
		if !hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return true when XDG_BIN_HOME is set")
		}
	})

	t.Run("XDG_DATA_HOME alone does not trigger", func(t *testing.T) {
		clearXDGEnv(t)
		t.Setenv("XDG_DATA_HOME", "/home/user/.local/share")
		if hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return false when only XDG_DATA_HOME is set")
		}
	})

	t.Run("XDG_CONFIG_HOME alone does not trigger", func(t *testing.T) {
		clearXDGEnv(t)
		t.Setenv("XDG_CONFIG_HOME", "/home/user/.config")
		if hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return false when only XDG_CONFIG_HOME is set")
		}
	})

	t.Run("XDG_RUNTIME_DIR alone does not trigger", func(t *testing.T) {
		clearXDGEnv(t)
		t.Setenv("XDG_RUNTIME_DIR", "/run/user/1000")
		if hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return false when only XDG_RUNTIME_DIR is set")
		}
	})

	t.Run("false by default", func(t *testing.T) {
		clearXDGEnv(t)
		if hasXDGEnv() {
			t.Fatal("expected hasXDGEnv() to return false with no env vars")
		}
	})
}

func TestInstallPathsAppliesDestdir(t *testing.T) {
	home := t.TempDir()
	clearInstallEnv(t, home)
	t.Setenv("BINDIR", filepath.Join("/opt", "mods", "bin"))
	t.Setenv("DESTDIR", filepath.Join(home, "pkgroot"))

	got, err := installPaths()
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(home, "pkgroot", "opt", "mods", "bin")
	if got.binDir != want {
		t.Fatalf("expected %q, got %q", want, got.binDir)
	}
}

func TestRunTask(t *testing.T) {
	t.Run("unknown task errors", func(t *testing.T) {
		err := runTask([]string{"nope"})
		if err == nil {
			t.Fatal("expected error for unknown task")
		}
	})

	t.Run("empty args errors", func(t *testing.T) {
		err := runTask(nil)
		if err == nil {
			t.Fatal("expected error for empty args")
		}
	})

	t.Run("too many args errors", func(t *testing.T) {
		err := runTask([]string{"build", "extra"})
		if err == nil {
			t.Fatal("expected error for too many args")
		}
	})
}

func clearInstallEnv(t *testing.T, home string) {
	t.Helper()

	for _, name := range append([]string{
		"BINDIR",
		"PREFIX",
		"DESTDIR",
	}, xdgEnvNames()...) {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func clearXDGEnv(t *testing.T) {
	t.Helper()

	for _, name := range xdgEnvNames() {
		t.Setenv(name, "")
	}
}

func xdgEnvNames() []string {
	return []string{
		"XDG",
		"XDG_BIN_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_CACHE_HOME",
		"XDG_STATE_HOME",
		"XDG_RUNTIME_DIR",
	}
}
