package app

import (
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestReviewPresentationSemanticTones(t *testing.T) {
	scope := WorkspaceScope("/workspace")
	tests := []struct {
		name     string
		tool     string
		args     string
		analysis approval.CommandAssessment
		intent   AccessIntent
		want     interactionTone
	}{
		{name: "delete", tool: "fs_delete_file", args: `{"path":"/workspace/file"}`, intent: AccessIntent{Class: AccessWrite}, want: interactionToneDanger},
		{name: "external read", tool: "fs_read_file", args: `{"path":"/etc/hosts"}`, intent: AccessIntent{Class: AccessRead, Dirs: []string{"/etc"}}, want: interactionToneInfo},
		{name: "workspace write", tool: "shell_run", args: `{"command":"touch file"}`, analysis: approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/workspace"}}, intent: AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}}, want: interactionToneWarning},
		{name: "sudo", tool: "shell_run", args: `{"command":"sudo rm /usr/local/bin/mods"}`, analysis: approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/usr/local/bin"}}, intent: AccessIntent{Class: AccessWrite, Dirs: []string{"/usr/local/bin"}}, want: interactionToneDanger},
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
	scope := WorkspaceScope("/workspace")
	internalLabels := map[string]bool{
		"Reason": true, "Reviewability": true, "Composition": true, "Suggestion": true,
		"Program": true, "Arguments": true, "Scope": true,
	}
	tests := []struct {
		name     string
		tool     string
		args     string
		analysis approval.CommandAssessment
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
			analysis: approval.CommandAssessment{
				Effect: approval.EffectWrite, KnownDirs: []string{"/workspace"}, Reason: "static classifier detail",
				Shape:         approval.CommandShape{TopLevelActions: 4, Pipelines: 2},
				Reviewability: approval.CommandReviewability{Level: approval.ReviewabilityCompound, ShouldCorrect: true},
			},
			intent: AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}}, maxRows: 2,
		},
		{
			name: "process", tool: "process_run", args: `{"program":"rm","args":["out"]}`,
			analysis: approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/workspace"}},
			intent:   AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}}, maxRows: 2,
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
