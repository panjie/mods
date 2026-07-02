package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

const (
	app    = "mods"
	binDir = "bin"
)

func main() {
	if err := runTask(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func runTask(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: go run ./internal/buildtask <build|release|install|uninstall|clean>")
	}

	switch args[0] {
	case "build":
		return build(binaryPath(), false)
	case "release":
		return build(binaryPath(), true)
	case "install":
		return install()
	case "uninstall":
		return uninstall()
	case "clean":
		return clean()
	default:
		return fmt.Errorf("unknown task %q", args[0])
	}
}

func install() error {
	if err := build(binaryPath(), false); err != nil {
		return err
	}

	paths, err := installPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.binDir, 0o755); err != nil {
		return err
	}
	return copyFile(binaryPath(), filepath.Join(paths.binDir, app+goExe()), 0o755)
}

func uninstall() error {
	paths, err := installPaths()
	if err != nil {
		return err
	}
	return removeFile(filepath.Join(paths.binDir, app+goExe()))
}

func clean() error {
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

var goExe = sync.OnceValue(func() string {
	out, err := exec.Command("go", "env", "GOEXE").Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
})

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
	binDir string
}

func installPaths() (installConfig, error) {
	bin, err := installDir()
	if err != nil {
		return installConfig{}, err
	}

	destdir := os.Getenv("DESTDIR")
	return installConfig{
		binDir: withDestDir(destdir, bin),
	}, nil
}

func installDir() (string, error) {
	if v := os.Getenv("BINDIR"); v != "" {
		return v, nil
	}
	if v := os.Getenv("PREFIX"); v != "" {
		return filepath.Join(v, "bin"), nil
	}
	if hasXDGEnv() {
		return xdgInstallDir()
	}
	return defaultInstallDir()
}

func defaultInstallDir() (string, error) {
	prefix := "/usr/local"
	if runtime.GOOS == "windows" {
		home, err := userHome()
		if err != nil {
			return "", err
		}
		prefix = filepath.Join(home, ".local")
	}
	return filepath.Join(prefix, "bin"), nil
}

func hasXDGEnv() bool {
	return os.Getenv("XDG") == "1" || os.Getenv("XDG_BIN_HOME") != ""
}

func xdgInstallDir() (string, error) {
	if bin := os.Getenv("XDG_BIN_HOME"); bin != "" {
		return bin, nil
	}

	home, err := userHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "bin"), nil
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
