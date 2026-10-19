package tools

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

type powerShellVersionResult struct {
	once    sync.Once
	version string
	err     error
}

// Both selected-shell metadata and the Windows capability catalog share this
// cache. Resolving names first deduplicates pwsh, pwsh.exe and absolute paths.
var powerShellVersions sync.Map

func cachedPowerShellVersion(ctx context.Context, cache *sync.Map, path string, probe func(context.Context, string) (string, error)) (string, error) {
	key := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		key = strings.ToLower(key)
	}
	entry, _ := cache.LoadOrStore(key, &powerShellVersionResult{})
	result := entry.(*powerShellVersionResult)
	result.once.Do(func() { result.version, result.err = probe(ctx, path) })
	return result.version, result.err
}

// PowerShellVersion returns one cached version probe per resolved executable
// for this process. The caller supplies the bounded probe context.
func PowerShellVersion(ctx context.Context, name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return cachedPowerShellVersion(ctx, &powerShellVersions, path, func(ctx context.Context, path string) (string, error) {
		cmd := exec.CommandContext(ctx, path, "-NoProfile", "-Command", "$PSVersionTable.PSVersion.ToString()")
		out, err := cmd.Output()
		if err != nil {
			return "", err
		}
		version := strings.TrimSpace(string(out))
		if version == "" {
			return "", fmt.Errorf("%s returned empty version output", path)
		}
		return version, nil
	})
}
