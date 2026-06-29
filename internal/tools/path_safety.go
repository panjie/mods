package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Workspace path resolution and safety. Every filesystem tool calls
// resolveWorkspacePath before touching the filesystem; the helpers here
// are the only thing standing between an LLM-authored path and an
// arbitrary location on disk, so they are deliberately defensive.

func resolveWorkspacePath(root, input string, safeDirs []string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("path is required")
	}
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)

	// boundary is the directory the resolved path must stay inside after
	// symlink evaluation. The default is the workspace root; if the input
	// instead lives under a configured safe directory (e.g. os.TempDir()),
	// that safe directory becomes the boundary so a symlink inside it
	// cannot escape to arbitrary paths like /etc/passwd.
	boundary := root
	if err := ensureInsideRoot(root, path); err != nil {
		safe, ok := matchSafeDir(path, safeDirs)
		if !ok {
			return "", err
		}
		boundary = safe
	}

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
	parts := append([]string{existingEval}, missing...)
	resolved := filepath.Join(parts...)

	// Compare the resolved path against the boundary after evaluating the
	// boundary's own symlinks (necessary on macOS where /tmp resolves to
	// /private/tmp; without this step a perfectly valid path under
	// /private/tmp/... would be reported as escaping /tmp).
	boundaryEval := boundary
	if absBoundary, absErr := filepath.Abs(boundary); absErr == nil {
		boundaryEval = absBoundary
	}
	if eval, evalErr := filepath.EvalSymlinks(boundaryEval); evalErr == nil {
		boundaryEval = eval
	}
	if err := ensureInsideRoot(boundaryEval, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// matchSafeDir returns the first safe directory that lexically contains
// path together with a bool indicator. It does not follow symlinks so the
// fallback path stays cheap; the symlink-aware boundary check happens
// after the caller's symlink resolution step.
func matchSafeDir(path string, safeDirs []string) (string, bool) {
	for _, safe := range safeDirs {
		rel, err := filepath.Rel(safe, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return safe, true
		}
	}
	return "", false
}

func workspaceRel(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

func ensureInsideRoot(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return err
	}
	if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
		return nil
	}
	return fmt.Errorf("path %q is outside workspace root; use shell_run to access paths outside the workspace", path)
}
