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
		"Arguments": true, "Scope": true,
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
			intent: AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/cwd"}}, maxRows: 5,
		},
		{
			name: "process", tool: "process_run", args: `{"program":"rm","args":["out"]}`,
			analysis: commandanalysis.CommandAssessment{Effect: commandanalysis.EffectWrite, KnownDirs: []string{"/cwd"}},
			intent:   AccessIntent{WriteCoverage: testCompleteCoverage, Class: AccessWrite, Dirs: []string{"/cwd"}}, maxRows: 6,
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
		name string
		cwd  string
		want string
	}{
		{name: "default cwd is explicit execution context", cwd: scope.Value, want: scope.Value},
		{name: "overridden cwd is explicit execution context", cwd: other, want: other},
	} {
		t.Run(tt.name, func(t *testing.T) {
			args := []byte(`{"command":"touch out","cwd":` + strconv.Quote(tt.cwd) + `}`)
			got := formatReviewPresentationWithIntent("shell_run", args, commandanalysis.CommandAssessment{}, scope, AccessIntent{WriteCoverage: testCompleteCoverage})
			require.Contains(t, got.rows, interactionRow{Label: "Working dir", Value: tt.want})
		})
	}
}

func TestCommandReviewDistinguishesUnknownEffectsFromIncompleteWrites(t *testing.T) {
	unknown := commandReviewRows("custom", commandanalysis.CommandAssessment{
		Effect: commandanalysis.EffectUnknown,
		Targets: []commandanalysis.Target{{
			Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetRead,
			Operation: commandanalysis.OperationRead, Path: "/cwd/input", State: commandanalysis.TargetResolved,
		}},
		WriteCoverage: commandanalysis.CoverageIncomplete,
	}, "unknown")
	require.Contains(t, unknown, interactionRow{Label: "Recognized", Value: "read filesystem"})
	require.Contains(t, unknown, interactionRow{Label: "Effect", Value: "Cannot establish whether additional side effects are present."})
	require.NotContains(t, unknown, interactionRow{Label: "Coverage", Value: "Write-target coverage is incomplete; approval applies to this call only."})

	write := commandReviewRows("remove out", commandanalysis.CommandAssessment{
		Effect: commandanalysis.EffectWrite,
		Targets: []commandanalysis.Target{{
			Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetWrite,
			Operation: commandanalysis.OperationRemoveEntry, Raw: "$TARGET", State: commandanalysis.TargetUnresolved,
		}},
		WriteCoverage: commandanalysis.CoverageIncomplete,
	}, "dynamic mutation")
	require.Contains(t, write, interactionRow{Label: "Coverage", Value: "Write-target coverage is incomplete; approval applies to this call only."})
	for _, row := range write {
		require.NotEqual(t, "Effect", row.Label)
		require.NotEqual(t, "Unknown effects", row.Label)
	}
}

func TestCommandReviewShowsBoundedStructureLocations(t *testing.T) {
	rows := commandReviewRows("first\nsecond\nthird", commandanalysis.CommandAssessment{
		Effect: commandanalysis.EffectUnknown,
		Structure: &commandanalysis.CommandStructure{OffsetUnit: "byte", Findings: []commandanalysis.StructureFinding{
			{Code: commandanalysis.StructureControlFlow, NodeID: 7, Start: 6, End: 12, Located: true},
			{Code: commandanalysis.StructureUnknownInvocation, NodeID: 8, Start: 999, End: 1000, Located: true},
			{Code: commandanalysis.StructureState},
		}},
	}, "unknown")
	require.Contains(t, rows, interactionRow{Label: "Analysis limit", Value: "control flow [line 2, node 7]"})
	require.Contains(t, rows, interactionRow{Label: "Analysis limit", Value: "unmodeled invocation [command level, node 8]"})
	require.Contains(t, rows, interactionRow{Label: "Analysis limit", Value: "local state assignment [command level]"})
}

func TestCommandReviewPreservesNumberedSourceAndLiteralArgv(t *testing.T) {
	shell := formatReviewPresentationWithIntent("powershell_run", []byte(`{"command":"Write-Output one\nWrite-Output two"}`), commandanalysis.CommandAssessment{}, WorkingDirScope("C:\\cwd"), AccessIntent{})
	require.Contains(t, shell.rows, interactionRow{Label: "Source", Value: "1 | Write-Output one\n2 | Write-Output two"})
	require.Contains(t, shell.rows, interactionRow{Label: "Interpreter", Value: "PowerShell"})
	require.Contains(t, shell.rows, interactionRow{Label: "Working dir", Value: "C:\\cwd"})

	process := formatReviewPresentationWithIntent("process_run", []byte(`{"program":"python","args":["script path.py","line 1\nline 2",""],"cwd":"C:\\cwd"}`), commandanalysis.CommandAssessment{}, WorkingDirScope("C:\\cwd"), AccessIntent{})
	require.Contains(t, process.rows, interactionRow{Label: "Program", Value: "python"})
	require.Contains(t, process.rows, interactionRow{Label: "Argv", Value: "0: \"python\"\n1: \"script path.py\"\n2: \"line 1\\nline 2\"\n3: \"\""})
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
