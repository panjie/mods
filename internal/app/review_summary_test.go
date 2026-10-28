package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatReviewSummaryReadOnly(t *testing.T) {
	scope := WorkingDirScope(t.TempDir())
	got := formatReviewSummaryWithIntent("fs_read_file", []byte(`{"path":"/etc/passwd"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage})
	require.Contains(t, got, "read-only")
	require.Contains(t, got, "/etc")
	require.NotContains(t, got, "passwd")
}

func TestSafeDirsIncludesTmp(t *testing.T) {
	// /tmp must be a safe dir on POSIX so reads/writes there don't trigger
	// approval. On macOS os.TempDir() is a per-user /var/folders path, not
	// /tmp, so without /tmp in safeDirs those operations get flagged.
	if runtime.GOOS == "windows" {
		t.Skip("/tmp is not a POSIX safe dir on Windows")
	}
	dirs := safeDirs()
	require.Contains(t, dirs, "/tmp")
	require.Contains(t, dirs, os.TempDir())
}

func TestFormatReviewSummaryWithIntent(t *testing.T) {
	t.Run("fs write shows create or overwrite", func(t *testing.T) {
		root := t.TempDir()
		scope := WorkingDirScope(root)
		createSummary := formatReviewSummaryWithIntent("fs_write_file", []byte(`{"path":"new.txt","content":"hello"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage})
		require.Contains(t, createSummary, "new.txt")
		require.Contains(t, createSummary, "creates new file")
		require.Contains(t, createSummary, "5 bytes")

		existing := filepath.Join(root, "existing.txt")
		require.NoError(t, os.WriteFile(existing, []byte("old"), 0o644))
		overwriteSummary := formatReviewSummaryWithIntent("fs_write_file", []byte(`{"path":"existing.txt","content":"hello"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage})
		require.Contains(t, overwriteSummary, "overwrites existing file")
	})

	t.Run("patch summarizes files and line counts", func(t *testing.T) {
		patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+new\n+more\n"
		summary := formatReviewSummaryWithIntent("fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`), testApprovalScope, AccessIntent{WriteCoverage: testCompleteCoverage})
		require.Equal(t, "Patch: a.txt (+2 -1)", summary)
	})

	t.Run("replace summarizes byte counts", func(t *testing.T) {
		summary := formatReviewSummaryWithIntent("fs_replace", []byte(`{"path":"a.txt","old_text":"old","new_text":"newer"}`), testApprovalScope, AccessIntent{WriteCoverage: testCompleteCoverage})
		require.Equal(t, "Target: a.txt - replace 3 bytes with 5 bytes", summary)
	})

	t.Run("new filesystem mutations summarize action", func(t *testing.T) {
		scope := WorkingDirScope(t.TempDir())
		require.Contains(t, formatReviewSummaryWithIntent("fs_delete_file", []byte(`{"path":"old.txt"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage}), "delete file")
		require.Contains(t, formatReviewSummaryWithIntent("fs_delete_dir", []byte(`{"path":"old-dir"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage}), "delete directory")
		require.Contains(t, formatReviewSummaryWithIntent("fs_mkdir", []byte(`{"path":"new-dir"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage}), "create directory")
		require.Contains(t, formatReviewSummaryWithIntent("fs_copy", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage}), "a.txt -> b.txt")
		require.Contains(t, formatReviewSummaryWithIntent("fs_move", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), scope, AccessIntent{WriteCoverage: testCompleteCoverage}), "a.txt -> b.txt")
	})

}
