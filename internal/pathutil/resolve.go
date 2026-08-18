package pathutil

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveThroughExistingParent resolves path through symlinks by walking up
// to the closest existing ancestor, evaluating that ancestor's symlinks, and
// re-appending the missing components. This lets callers canonicalize paths
// that do not exist yet (e.g. a write target) while still resolving every
// symlink along the existing prefix, so a symlink cannot smuggle a path
// outside its intended boundary.
func ResolveThroughExistingParent(path string) (string, error) {
	existing := path
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("could not find existing parent for %q", path)
		}
		missing = append([]string{filepath.Base(existing)}, missing...)
		existing = parent
	}

	existingEval, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", err
	}
	existingEval, err = resolvePlatformFinalPath(existingEval)
	if err != nil {
		return "", err
	}
	parts := append([]string{existingEval}, missing...)
	return filepath.Join(parts...), nil
}
