package pathutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func evalOrSelf(t *testing.T, p string) string {
	t.Helper()
	eval, err := filepath.EvalSymlinks(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return filepath.Clean(eval)
}

func TestResolveThroughExistingParent(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "sub")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "data.txt"), []byte("x"), 0o600))

	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(sub, alias); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}

	t.Run("existing file through symlinked dir resolves", func(t *testing.T) {
		got, err := ResolveThroughExistingParent(filepath.Join(alias, "data.txt"))
		require.NoError(t, err)
		require.Equal(t, evalOrSelf(t, filepath.Join(sub, "data.txt")), got)
	})

	t.Run("missing leaf re-appended after symlink resolution", func(t *testing.T) {
		got, err := ResolveThroughExistingParent(filepath.Join(alias, "new.txt"))
		require.NoError(t, err)
		require.Equal(t, filepath.Join(evalOrSelf(t, sub), "new.txt"), got)
	})

	t.Run("missing directory chain walks to existing ancestor", func(t *testing.T) {
		got, err := ResolveThroughExistingParent(filepath.Join(alias, "a", "b", "c.txt"))
		require.NoError(t, err)
		require.Equal(t, filepath.Join(evalOrSelf(t, sub), "a", "b", "c.txt"), got)
	})
}

func TestLocationResolvesWorkspaceSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin on Windows")
	}
	realRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "lisp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "lisp", "init.el"), []byte("x"), 0o600))

	// The workspace is reached through a symlink alias, mirroring
	// ~/.emacs.d -> /real/dot.emacs.d. The scope is the canonical path.
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	target := filepath.Join(aliasRoot, "lisp", "init.el")
	require.Equal(t, LocationExternal, Location(target, "unrelated-lexical-scope", nil))
	require.Equal(t, LocationWorkspace, Location(target, realRoot, nil))
}

func TestLocationResolvesSafeDirSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin on Windows")
	}
	realSafe := t.TempDir()
	aliasSafe := filepath.Join(t.TempDir(), "alias-safe")
	if err := os.Symlink(realSafe, aliasSafe); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	target := filepath.Join(aliasSafe, "data.txt")
	require.Equal(t, LocationSafe, Location(target, t.TempDir(), []string{realSafe}))
}

func TestLocationDanglingSymlinkFallsBackLexical(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires admin on Windows")
	}
	parent := t.TempDir()
	if err := os.Symlink(filepath.Join(parent, "no-such-target"), filepath.Join(parent, "dangling")); err != nil {
		t.Skipf("symlink creation not supported: %v", err)
	}

	target := filepath.Join(parent, "dangling", "file.txt")
	require.Equal(t, LocationExternal, Location(target, filepath.Join(parent, "workspace"), nil))
}
