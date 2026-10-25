package app

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

func TestReviewPresentationSemanticTones(t *testing.T) {
	scope := WorkingDirScope("/cwd")
	tests := []struct {
		name     string
		tool     string
		args     string
		analysis commandanalysis.CommandAssessment
		intent   AccessIntent
		want     interactionTone
	}{
		{name: "delete", tool: "fs_delete_file", args: `{"path":"/cwd/file"}`, intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite}, want: interactionToneDanger},
		{name: "external read", tool: "fs_read_file", args: `{"path":"/etc/hosts"}`, intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessRead, Dirs: []string{"/etc"}}, want: interactionToneInfo},
		{name: "cwd write", tool: "shell_run", args: `{"command":"touch file"}`, analysis: commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, KnownDirs: []string{"/cwd"}}, intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/cwd"}}, want: interactionToneWarning},
		{name: "remote write", tool: "shell_run", args: `{"command":"curl -X DELETE https://example.com/item"}`, analysis: commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, RemoteOrigins: []string{"https://example.com"}}, intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, RemoteOrigins: []string{"https://example.com"}}, want: interactionToneWarning},
		{name: "sudo", tool: "shell_run", args: `{"command":"sudo rm /usr/local/bin/mods"}`, analysis: commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, KnownDirs: []string{"/usr/local/bin"}}, intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/usr/local/bin"}}, want: interactionToneDanger},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatReviewPresentationWithIntent(tt.tool, []byte(tt.args), tt.analysis, scope, tt.intent)
			require.Equal(t, tt.want, got.tone)
			require.NotEmpty(t, got.headline)
		})
	}
}

func TestReviewPresentationsStayConcise(t *testing.T) {
	scope := WorkingDirScope("/cwd")
	internalLabels := map[string]bool{
		"Reason": true, "Reviewability": true, "Composition": true, "Suggestion": true,
		"Program": true, "Arguments": true, "Scope": true,
	}
	tests := []struct {
		name     string
		tool     string
		args     string
		analysis commandanalysis.CommandAssessment
		intent   AccessIntent
		maxRows  int
	}{
		{name: "write", tool: "fs_write_file", args: `{"path":"new.txt","content":"hello"}`, maxRows: 1},
		{name: "replace", tool: "fs_replace", args: `{"path":"a.txt","old_text":"a","new_text":"b"}`, maxRows: 1},
		{name: "delete", tool: "fs_delete_file", args: `{"path":"a.txt"}`, maxRows: 1},
		{name: "copy", tool: "fs_copy", args: `{"source_path":"a","dest_path":"b"}`, maxRows: 2},
		{name: "patch", tool: "fs_apply_patch", args: `{"patch":"*** Begin Patch\n*** End Patch"}`, maxRows: 1},
		{
			name: "shell", tool: "shell_run", args: `{"command":"touch out"}`,
			analysis: commandanalysis.CommandAssessment{
				Effect: commandanalysis.EffectWrite, KnownDirs: []string{"/cwd"}, Reason: "static classifier detail",
				Shape:         commandanalysis.CommandShape{TopLevelActions: 4, Pipelines: 2},
				Reviewability: commandanalysis.CommandReviewability{Level: commandanalysis.ReviewabilityCompound},
			},
			intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/cwd"}}, maxRows: 2,
		},
		{
			name: "process", tool: "process_run", args: `{"program":"rm","args":["out"]}`,
			analysis: commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, KnownDirs: []string{"/cwd"}},
			intent:   AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/cwd"}}, maxRows: 2,
		},
		{name: "custom", tool: "mcp_custom", args: `{"query":"status","verbose":true}`, maxRows: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatReviewPresentationWithIntent(tt.tool, []byte(tt.args), tt.analysis, scope, tt.intent)
			require.LessOrEqual(t, len(got.rows), tt.maxRows)
			for _, row := range got.rows {
				require.Falsef(t, internalLabels[row.Label], "internal or redundant row %q leaked into review", row.Label)
			}
		})
	}
}

func TestReviewPresentationWorkingDirRow(t *testing.T) {
	scope := WorkingDirScope("/cwd")
	other := filepath.Join(scope.Value, "other")
	for _, tt := range []struct {
		name    string
		cwd     string
		wantRow bool
	}{
		{name: "cwd equals scope is redundant", cwd: scope.Value, wantRow: false},
		{name: "cwd outside scope stays visible", cwd: other, wantRow: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := []byte(`{"command":"touch out","cwd":` + strconv.Quote(tt.cwd) + `}`)
			got := formatReviewPresentationWithIntent("shell_run", args, commandanalysis.CommandAssessment{}, scope, AccessIntent{WriteCoverage: testCompleteCoverage})
			row := -1
			for i, r := range got.rows {
				if r.Label == "Working dir" {
					row = i
				}
			}
			if tt.wantRow {
				require.GreaterOrEqual(t, row, 0, "a distinct working dir is relevant review context")
			} else {
				require.Equal(t, -1, row, "the session working dir is already the approval scope")
			}
		})
	}
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
