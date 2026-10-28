package app

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewPresentationWorkingDirRow(t *testing.T) {
	scope := WorkingDirScope("/cwd")
	other := filepath.Join(scope.Value, "other")
	for _, tt := range []struct {
		name string
		cwd  string
		want string
	}{
		{name: "default cwd is explicit execution context", cwd: scope.Value, want: scope.Value},
		{name: "overridden cwd is explicit execution context", cwd: other, want: other},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := []byte(`{"command":"touch out","cwd":` + strconv.Quote(tt.cwd) + `}`)
			got := formatReviewPresentationWithIntent("shell_run", args, scope, AccessIntent{WriteCoverage: testCompleteCoverage})
			require.Contains(t, got.rows, interactionRow{Label: "Working dir", Value: tt.want})
		})
	}
}

func TestCommandReviewPreservesNumberedSourceAndLiteralArgv(t *testing.T) {
	shell := formatReviewPresentationWithIntent("powershell_run", []byte(`{"command":"Write-Output one\nWrite-Output two"}`), WorkingDirScope("C:\\cwd"), AccessIntent{})
	require.Contains(t, shell.rows, interactionRow{Label: "Command", Value: "1 | Write-Output one\n2 | Write-Output two"})
	require.Contains(t, shell.rows, interactionRow{Label: "Working dir", Value: "C:\\cwd"})

	process := formatReviewPresentationWithIntent("process_run", []byte(`{"program":"python","args":["script path.py","line 1\nline 2",""],"cwd":"C:\\cwd"}`), WorkingDirScope("C:\\cwd"), AccessIntent{})
	require.Contains(t, process.rows, interactionRow{Label: "Command", Value: "python \"script path.py\" \"line 1\\nline 2\" \"\""})

	git := formatReviewPresentationWithIntent("process_run", []byte(`{"program":"git","args":["add","-A"]}`), WorkingDirScope("/cwd"), AccessIntent{})
	require.Contains(t, git.rows, interactionRow{Label: "Command", Value: "git add -A"})
}

func TestReviewOrdersTargetsWithoutBreakingDownloadPairs(t *testing.T) {
	rows := []interactionRow{{Label: "Command", Value: "write"}, {Label: "Target", Value: "/tmp/project"}, {Label: "Remote", Value: "https://example.com"}}
	ordered := reviewOrderedRows(rows)
	require.Equal(t, "Target", ordered[0].Label)
	require.Equal(t, "Command", ordered[2].Label)
	require.Equal(t, "Command", rows[0].Label, "presentation must not mutate the original")
	downloads := []interactionRow{{Label: "File 1", Value: "a"}, {Label: "Source", Value: "https://a.example"}, {Label: "File 2", Value: "b"}, {Label: "Source", Value: "https://b.example"}}
	require.Equal(t, downloads, reviewOrderedRows(downloads))
}
