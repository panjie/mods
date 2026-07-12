package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReviewPresentationSemanticTones(t *testing.T) {
	scope := WorkspaceScope("/workspace")
	tests := []struct {
		name     string
		tool     string
		args     string
		analysis shellCommandAnalysis
		intent   AccessIntent
		want     interactionTone
	}{
		{name: "delete", tool: "fs_delete_file", args: `{"path":"/workspace/file"}`, intent: AccessIntent{Class: AccessWrite}, want: interactionToneDanger},
		{name: "external read", tool: "fs_read_file", args: `{"path":"/etc/hosts"}`, intent: AccessIntent{Class: AccessRead, Dirs: []string{"/etc"}}, want: interactionToneInfo},
		{name: "workspace write", tool: "shell_run", args: `{"command":"touch file"}`, analysis: shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"/workspace"}}, intent: AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}}, want: interactionToneWarning},
		{name: "sudo", tool: "shell_run", args: `{"command":"sudo rm /usr/local/bin/mods"}`, analysis: shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"/usr/local/bin"}}, intent: AccessIntent{Class: AccessWrite, Dirs: []string{"/usr/local/bin"}}, want: interactionToneDanger},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatReviewPresentationWithIntent(tt.tool, []byte(tt.args), tt.analysis, scope, tt.intent)
			require.Equal(t, tt.want, got.tone)
			require.NotEmpty(t, got.headline)
		})
	}
}
