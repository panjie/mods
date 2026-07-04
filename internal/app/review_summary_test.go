package app

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatReviewSummary(t *testing.T) {
	t.Run("fs write shows create or overwrite", func(t *testing.T) {
		root := t.TempDir()
		scope := WorkspaceScope(root)
		createSummary := formatReviewSummary("fs_write_file", []byte(`{"path":"new.txt","content":"hello"}`), shellCommandAnalysis{}, scope)
		require.Contains(t, createSummary, "new.txt")
		require.Contains(t, createSummary, "creates new file")
		require.Contains(t, createSummary, "5 bytes")

		existing := filepath.Join(root, "existing.txt")
		require.NoError(t, os.WriteFile(existing, []byte("old"), 0o644))
		overwriteSummary := formatReviewSummary("fs_write_file", []byte(`{"path":"existing.txt","content":"hello"}`), shellCommandAnalysis{}, scope)
		require.Contains(t, overwriteSummary, "overwrites existing file")
	})

	t.Run("patch summarizes files and line counts", func(t *testing.T) {
		patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+new\n+more\n"
		summary := formatReviewSummary("fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`), shellCommandAnalysis{}, testApprovalScope)
		require.Equal(t, "Patch: a.txt (+2 -1)", summary)
	})

	t.Run("new filesystem mutations summarize action", func(t *testing.T) {
		scope := WorkspaceScope(t.TempDir())
		require.Contains(t, formatReviewSummary("fs_delete_file", []byte(`{"path":"old.txt"}`), shellCommandAnalysis{}, scope), "delete file")
		require.Contains(t, formatReviewSummary("fs_delete_dir", []byte(`{"path":"old-dir"}`), shellCommandAnalysis{}, scope), "delete directory")
		require.Contains(t, formatReviewSummary("fs_mkdir", []byte(`{"path":"new-dir"}`), shellCommandAnalysis{}, scope), "create directory")
		require.Contains(t, formatReviewSummary("fs_copy", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), shellCommandAnalysis{}, scope), "a.txt -> b.txt")
		require.Contains(t, formatReviewSummary("fs_move", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), shellCommandAnalysis{}, scope), "a.txt -> b.txt")
	})

	t.Run("shell risk uses affected dirs", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"ls"}`), shellCommandAnalysis{NeedsReview: false}, scope),
			"read-only",
		)
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"touch out"}`), shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"."}}, scope),
			"workspace mutation",
		)
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"rm /tmp/x"}`), shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"/tmp"}}, scope),
			"external mutation",
		)
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"unknown"}`), shellCommandAnalysis{NeedsReview: true}, scope),
			"unknown",
		)
	})
}

func TestFormatReviewSummaryExternalRead(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	got := formatReviewSummary("fs_read_file", []byte(`{"path":"/etc/passwd"}`), shellCommandAnalysis{}, scope)
	require.Contains(t, got, "external read")
	require.Contains(t, got, "/etc")
	require.NotContains(t, got, "passwd")
}
