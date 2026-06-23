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

	t.Run("xdg data home enables local bin", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}

		want := filepath.Join(home, ".local", "bin")
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
		}
	})

	t.Run("xdg bin home wins over local bin", func(t *testing.T) {
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

	t.Run("legacy xdg switch enables local bin", func(t *testing.T) {
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

	t.Run("prefix wins over xdg", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
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

	t.Run("bindir wins over prefix and xdg", func(t *testing.T) {
		home := t.TempDir()
		clearInstallEnv(t, home)
		want := filepath.Join(home, "bin")
		t.Setenv("BINDIR", want)
		t.Setenv("PREFIX", filepath.Join(home, "prefix"))
		t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

		got, err := installDir()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("expected %q, got %q", want, got)
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

	want := withDestDir(filepath.Join(home, "pkgroot"), filepath.Join("/opt", "mods", "bin"))
	if got.binDir != want {
		t.Fatalf("expected %q, got %q", want, got.binDir)
	}
}

func clearInstallEnv(t *testing.T, home string) {
	t.Helper()

	for _, name := range []string{
		"BINDIR",
		"PREFIX",
		"DESTDIR",
		"XDG",
		"XDG_BIN_HOME",
		"XDG_CONFIG_HOME",
		"XDG_DATA_HOME",
		"XDG_CACHE_HOME",
		"XDG_STATE_HOME",
		"XDG_RUNTIME_DIR",
	} {
		t.Setenv(name, "")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}
