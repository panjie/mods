package testutil

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// CanonicalTempDir returns a temporary directory with symlinks resolved.
// Production code canonicalizes paths before authorization, and macOS resolves
// its per-user temporary directory under /private/var, so tests that compare
// production paths with fixture paths must use the same canonical form.
func CanonicalTempDir(tb testing.TB) string {
	tb.Helper()
	canonical, err := filepath.EvalSymlinks(tb.TempDir())
	require.NoError(tb, err)
	return canonical
}
