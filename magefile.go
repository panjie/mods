//go:build mage

package main

import (
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const (
	app    = "mods"
	binDir = "bin"
	manDir = "manpages"
)

var Default = Build

var Aliases = map[string]interface{}{
	"clean-man": CleanMan,
}

// Build compiles the CLI with version metadata.
func Build() error {
	return build(binaryPath(), false)
}

// Release builds a stripped release binary with version metadata.
func Release() error {
	return build(binaryPath(), true)
}

// Check verifies that all packages compile.
func Check() error {
	return run("go", "build", "./...")
}

// Test runs the Go test suite.
func Test() error {
	return run("go", "test", "./...")
}

// Man generates the local manpage artifact.
func Man() error {
	if err := os.MkdirAll(manDir, 0o755); err != nil {
		return err
	}

	out, err := output("go", "run", ".", "man")
	if err != nil {
		return err
	}

	if runtime.GOOS == "windows" {
		return os.WriteFile(manPagePath(), out, 0o644)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(out); err != nil {
		_ = gz.Close()
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(manPagePath(), buf.Bytes(), 0o644)
}

// Install builds and installs the binary and manpage.
func Install() error {
	if err := Build(); err != nil {
		return err
	}
	if err := Man(); err != nil {
		return err
	}

	paths, err := installPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.binDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(paths.man1Dir, 0o755); err != nil {
		return err
	}
	if err := copyFile(binaryPath(), filepath.Join(paths.binDir, app+goExe()), 0o755); err != nil {
		return err
	}
	return copyFile(manPagePath(), filepath.Join(paths.man1Dir, filepath.Base(manPagePath())), 0o644)
}

// Uninstall removes installed files.
func Uninstall() error {
	paths, err := installPaths()
	if err != nil {
		return err
	}
	if err := removeFile(filepath.Join(paths.binDir, app+goExe())); err != nil {
		return err
	}
	return removeFile(filepath.Join(paths.man1Dir, filepath.Base(manPagePath())))
}

// CleanMan removes the generated manpage artifact.
func CleanMan() error {
	return removeFile(manPagePath())
}

// Clean removes build artifacts.
func Clean() error {
	return os.RemoveAll(binDir)
}

func build(out string, release bool) error {
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}

	ldflags := fmt.Sprintf("-X main.Version=%s -X main.CommitSHA=%s", gitVersion(), gitCommit())
	if release {
		ldflags = "-s -w " + ldflags
	}

	return run("go", "build", "-trimpath", "-ldflags="+ldflags, "-o", out, ".")
}

func binaryPath() string {
	return filepath.Join(binDir, app+goExe())
}

func manPagePath() string {
	name := app + ".1"
	if runtime.GOOS != "windows" {
		name += ".gz"
	}
	return filepath.Join(manDir, name)
}

func goExe() string {
	out, err := exec.Command("go", "env", "GOEXE").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func gitVersion() string {
	return gitValue("describe", "--tags", "--always", "--dirty")
}

func gitCommit() string {
	return gitValue("rev-parse", "--short", "HEAD")
}

func gitValue(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return "unknown"
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return "unknown"
	}
	return value
}

type installConfig struct {
	binDir  string
	man1Dir string
}

func installPaths() (installConfig, error) {
	bin, man, err := defaultInstallDirs()
	if err != nil {
		return installConfig{}, err
	}
	if v := os.Getenv("BINDIR"); v != "" {
		bin = v
	}
	if v := os.Getenv("MANDIR"); v != "" {
		man = v
	}

	destdir := os.Getenv("DESTDIR")
	return installConfig{
		binDir:  withDestDir(destdir, bin),
		man1Dir: withDestDir(destdir, filepath.Join(man, "man1")),
	}, nil
}

func defaultInstallDirs() (string, string, error) {
	if os.Getenv("XDG") == "1" {
		return xdgInstallDirs()
	}

	prefix := os.Getenv("PREFIX")
	if prefix == "" {
		if runtime.GOOS == "windows" {
			home, err := userHome()
			if err != nil {
				return "", "", err
			}
			prefix = filepath.Join(home, ".local")
		} else {
			prefix = "/usr/local"
		}
	}

	return filepath.Join(prefix, "bin"), filepath.Join(prefix, "share", "man"), nil
}

func xdgInstallDirs() (string, string, error) {
	bin := os.Getenv("XDG_BIN_HOME")
	data := os.Getenv("XDG_DATA_HOME")

	if runtime.GOOS == "windows" {
		if bin == "" {
			return "", "", errors.New("XDG=1 on Windows requires XDG_BIN_HOME")
		}
		if data == "" {
			return "", "", errors.New("XDG=1 on Windows requires XDG_DATA_HOME")
		}
		return bin, filepath.Join(data, "man"), nil
	}

	home, err := userHome()
	if err != nil {
		return "", "", err
	}
	if bin == "" {
		bin = filepath.Join(home, ".local", "bin")
	}
	if data == "" {
		data = filepath.Join(home, ".local", "share")
	}
	return bin, filepath.Join(data, "man"), nil
}

func userHome() (string, error) {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home, nil
	}
	return "", errors.New("could not determine user home directory")
}

func withDestDir(destdir, path string) string {
	path = filepath.FromSlash(path)
	if destdir == "" {
		return filepath.Clean(path)
	}

	destdir = filepath.FromSlash(destdir)
	volume := filepath.VolumeName(path)
	rest := strings.TrimPrefix(path, volume)
	rest = strings.TrimLeft(rest, `\/`)
	return filepath.Join(destdir, rest)
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Chmod(dst, perm)
}

func removeFile(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func output(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, stderr.String())
	}
	return out, nil
}
