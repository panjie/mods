package approval

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// SafeDirs returns the filesystem directories that may be accessed without
// explicit approval. Both the approval matrix and filesystem tool enforcement
// must consume this function so a path cannot be approved by one layer and
// rejected by the other.
func SafeDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		// On macOS os.TempDir() is a per-user /var/folders path, while /tmp is
		// the conventional scratch location models and users commonly choose.
		dirs = append(dirs, "/tmp")
	}
	return dirs
}

// SafeDirsWith returns the default safe directories plus extra approval-safe
// directories. The result is de-duplicated while preserving the default safe
// directory order followed by the caller-provided order.
func SafeDirsWith(extra []string) []string {
	dirs := SafeDirs()
	if len(extra) == 0 {
		return dirs
	}
	seen := make(map[string]struct{}, len(dirs)+len(extra))
	out := make([]string, 0, len(dirs)+len(extra))
	add := func(dir string) {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			return
		}
		if abs, err := filepath.Abs(dir); err == nil {
			dir = abs
		}
		dir = filepath.Clean(dir)
		key := filepath.ToSlash(dir)
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, dir)
	}
	for _, dir := range dirs {
		add(dir)
	}
	for _, dir := range extra {
		add(dir)
	}
	return out
}
