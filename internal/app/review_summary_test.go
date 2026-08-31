package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestFormatReviewSummary(t *testing.T) {
	t.Run("fs write shows create or overwrite", func(t *testing.T) {
		root := t.TempDir()
		scope := WorkspaceScope(root)
		createSummary := formatReviewSummary("fs_write_file", []byte(`{"path":"new.txt","content":"hello"}`), approval.CommandAssessment{}, scope)
		require.Contains(t, createSummary, "new.txt")
		require.Contains(t, createSummary, "creates new file")
		require.Contains(t, createSummary, "5 bytes")

		existing := filepath.Join(root, "existing.txt")
		require.NoError(t, os.WriteFile(existing, []byte("old"), 0o644))
		overwriteSummary := formatReviewSummary("fs_write_file", []byte(`{"path":"existing.txt","content":"hello"}`), approval.CommandAssessment{}, scope)
		require.Contains(t, overwriteSummary, "overwrites existing file")
	})

	t.Run("patch summarizes files and line counts", func(t *testing.T) {
		patch := "--- a/a.txt\n+++ b/a.txt\n@@ -1 +1,2 @@\n-old\n+new\n+more\n"
		summary := formatReviewSummary("fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`), approval.CommandAssessment{}, testApprovalScope)
		require.Equal(t, "Patch: a.txt (+2 -1)", summary)
	})

	t.Run("replace summarizes byte counts", func(t *testing.T) {
		summary := formatReviewSummary("fs_replace", []byte(`{"path":"a.txt","old_text":"old","new_text":"newer"}`), approval.CommandAssessment{}, testApprovalScope)
		require.Equal(t, "Target: a.txt - replace 3 bytes with 5 bytes", summary)
	})

	t.Run("new filesystem mutations summarize action", func(t *testing.T) {
		scope := WorkspaceScope(t.TempDir())
		require.Contains(t, formatReviewSummary("fs_delete_file", []byte(`{"path":"old.txt"}`), approval.CommandAssessment{}, scope), "delete file")
		require.Contains(t, formatReviewSummary("fs_delete_dir", []byte(`{"path":"old-dir"}`), approval.CommandAssessment{}, scope), "delete directory")
		require.Contains(t, formatReviewSummary("fs_mkdir", []byte(`{"path":"new-dir"}`), approval.CommandAssessment{}, scope), "create directory")
		require.Contains(t, formatReviewSummary("fs_copy", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), approval.CommandAssessment{}, scope), "a.txt -> b.txt")
		require.Contains(t, formatReviewSummary("fs_move", []byte(`{"source_path":"a.txt","dest_path":"b.txt"}`), approval.CommandAssessment{}, scope), "a.txt -> b.txt")
	})

	t.Run("shell risk uses affected dirs", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"ls"}`), approval.CommandAssessment{Effect: approval.EffectRead}, scope),
			"read-only",
		)
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"touch out"}`), approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"."}}, scope),
			"workspace mutation",
		)
		unknownSummary := formatReviewSummary("shell_run", []byte(`{"command":"opaque-command"}`), approval.CommandAssessment{Effect: approval.EffectUnknown, KnownDirs: []string{"/workspace"}}, scope)
		require.Contains(t, unknownSummary, "unknown")
		require.NotContains(t, unknownSummary, "workspace mutation")
		contradictoryWrite := approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/workspace"}}
		contradictoryWriteSummary := formatReviewSummary("shell_run", []byte(`{"command":"touch out"}`), contradictoryWrite, scope)
		require.Contains(t, contradictoryWriteSummary, "workspace mutation")
		require.NotContains(t, contradictoryWriteSummary, "read-only")
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"rm /tmp/x"}`), approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/tmp"}}, scope),
			"external mutation",
		)
		require.Contains(t,
			formatReviewSummary("shell_run", []byte(`{"command":"unknown"}`), approval.CommandAssessment{Effect: approval.EffectWrite}, scope),
			"unknown-location mutation",
		)
	})

	t.Run("process risk uses argv preview", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		analysis := approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/workspace"}}
		args := []byte(`{"program":"rm","args":["path with space"]}`)
		summary := formatReviewSummary("process_run", args, analysis, scope)
		require.Contains(t, summary, "workspace mutation")
		require.Contains(t, formatReviewLabel("process_run", args), `rm "path with space"`)
		presentation := formatReviewPresentationWithIntent("process_run", args, analysis, scope, AccessIntent{Class: AccessWrite, Dirs: []string{"/workspace"}})
		require.Equal(t, "Modify workspace files", presentation.headline)
		require.Equal(t, []interactionRow{
			{Label: "Command", Value: `rm "path with space"`},
			{Label: "Target", Value: "/workspace"},
		}, presentation.rows)
	})

	t.Run("shell risk hides speculative LLM reason when dirs unknown", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		got := formatReviewSummary("shell_run", []byte(`{"command":"scoop install nodejs"}`),
			approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "installs nodejs via scoop"}, scope)
		require.Contains(t, got, "unknown-location mutation")
		require.NotContains(t, got, "installs nodejs via scoop")
	})

	t.Run("shell risk surfaces LLM reason when dirs present", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		got := formatReviewSummary("shell_run", []byte(`{"command":"scoop install nodejs"}`),
			approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/home/user/scoop"}, Reason: "modifies system state"}, scope)
		require.Contains(t, got, "external mutation")
		require.Contains(t, got, "modifies system state")
	})

	t.Run("shell risk omits reason when empty", func(t *testing.T) {
		scope := WorkspaceScope("/workspace")
		got := formatReviewSummary("shell_run", []byte(`{"command":"unknown"}`),
			approval.CommandAssessment{Effect: approval.EffectWrite}, scope)
		require.NotContains(t, got, "(")
	})
}

func TestDynamicShellTargetPresentation(t *testing.T) {
	scope := WorkspaceScope(`C:\Users\panjie\dev\mods`)
	analysis := approval.CommandAssessment{
		Effect:         approval.EffectWrite,
		DynamicTargets: []string{`$PROFILE.CurrentUserCurrentHost`, `$prof`},
		Reason:         "writes PowerShell profile",
	}
	args := []byte(`{"command":"Set-Content -Path $prof -Value x"}`)
	intent := AccessIntent{Class: AccessWrite, UnresolvedPaths: analysis.DynamicTargets}

	presentation := formatReviewPresentationWithIntent("powershell_run", args, analysis, scope, intent)
	require.Equal(t, "Modify a dynamic target", presentation.headline)
	require.Equal(t, interactionToneDanger, presentation.tone)
	require.Contains(t, presentation.rows, interactionRow{Label: "Target", Value: `$PROFILE.CurrentUserCurrentHost, $prof`})

	summary := formatReviewSummaryWithIntent("powershell_run", args, analysis, scope, intent)
	require.Contains(t, summary, "dynamic mutation")
	require.Contains(t, summary, "$PROFILE.CurrentUserCurrentHost")

	rules := candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto), true)
	require.Empty(t, rules, "runtime-resolved targets must never offer a persistent directory rule")
}

func TestDynamicReadShellTargetPresentation(t *testing.T) {
	scope := WorkspaceScope(`C:\Users\panjie\dev\mods`)
	analysis := approval.CommandAssessment{
		Effect:         approval.EffectRead,
		KnownDirs:      []string{`C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links`},
		DynamicTargets: []string{`$v`},
		Reason:         "inspects PowerShell profile paths",
	}
	args := []byte(`{"command":"Test-Path $v"}`)
	intent := AccessIntent{Class: AccessRead, Dirs: analysis.KnownDirs, UnresolvedPaths: analysis.DynamicTargets}

	presentation := formatReviewPresentationWithIntent("powershell_run", args, analysis, scope, intent)
	require.Equal(t, "Read a dynamic target", presentation.headline)
	require.Equal(t, interactionToneInfo, presentation.tone)
	require.Contains(t, presentation.rows, interactionRow{
		Label: "Target",
		Value: `$v · known: C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links`,
	})

	summary := formatReviewSummaryWithIntent("powershell_run", args, analysis, scope, intent)
	require.Contains(t, summary, "dynamic read")
	require.Contains(t, summary, `$v`)

	require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	rules := candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto), true)
	require.NotEmpty(t, rules, "a dynamic read offers the explicit-consent DynamicReadAllow rule")
	labels := RulesLabel(rules)
	require.Contains(t, labels, "reads of runtime-resolved targets")
	require.Contains(t, labels, `read dirs: C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links`,
		"concrete external dirs still offer their scoped DirAllow rule")
}

func TestNonPathShapedDynamicTargetHiddenFromReviewPanel(t *testing.T) {
	scope := WorkspaceScope(`C:\Users\panjie\dev\mods`)
	analysis := approval.CommandAssessment{
		Effect:         approval.EffectRead,
		DynamicTargets: []string{`(json-insert (emacs-startup-usage))`},
		Reason:         "measures emacs startup",
	}
	args := []byte(`{"command":"& emacs --batch --eval \"(json-insert (emacs-startup-usage))\""}`)
	intent := AccessIntent{Class: AccessRead, UnresolvedPaths: analysis.DynamicTargets}

	presentation := formatReviewPresentationWithIntent("powershell_run", args, analysis, scope, intent)
	require.Equal(t, "Read a dynamic target", presentation.headline)
	require.NotContains(t, presentation.rows, interactionRow{Label: "Target", Value: `(json-insert (emacs-startup-usage))`},
		"non-path-shaped dynamic targets must not be shown as a review target")
}

func TestCompoundShellReviewKeepsInternalAnalysisOutOfPanel(t *testing.T) {
	analysis := approval.CommandAssessment{
		Shape: approval.CommandShape{TopLevelActions: 4, Pipelines: 2},
		Reviewability: approval.CommandReviewability{
			Level:         approval.ReviewabilityCompound,
			Reasons:       []approval.ReviewabilityReason{approval.ReviewabilityMultipleIndependent},
			ShouldCorrect: true,
		},
	}
	presentation := formatReviewPresentationWithIntent(
		"powershell_run",
		[]byte(`{"command":"Write-Output a; Write-Output b"}`),
		analysis,
		WorkspaceScope(`C:\workspace`),
		AccessIntent{Class: AccessWrite},
	)
	require.Equal(t, []interactionRow{
		{Label: "Command", Value: "Write-Output a; Write-Output b"},
		{Label: "Target", Value: "Unknown"},
	}, presentation.rows)
}

func TestFormatReviewSummaryExternalRead(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	got := formatReviewSummary("fs_read_file", []byte(`{"path":"/etc/passwd"}`), approval.CommandAssessment{}, scope)
	require.Contains(t, got, "external read")
	require.Contains(t, got, "/etc")
	require.NotContains(t, got, "passwd")
}

func TestFormatReviewLabelReadTools(t *testing.T) {
	// Regression: read tools used to fall through to the generic
	// "Execute <name> (<args>)" label while write/delete/move/copy/shell had
	// dedicated labels. Read tools must now use dedicated terse labels too.
	cases := []struct {
		name   string
		args   string
		wantIn string
	}{
		{"fs_read_file", `{"path":"src/foo.go"}`, "Read"},
		{"fs_list_dir", `{"path":"src"}`, "List"},
		{"fs_stat", `{"path":"a.txt"}`, "Stat"},
		{"fs_search", `{"path":"src","query":"foo"}`, "Search"},
		{"fs_largest", `{"path":"."}`, "Largest"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := formatReviewLabel(c.name, []byte(c.args))
			require.Contains(t, got, c.wantIn)
			require.NotContains(t, got, "Execute", "read tool must not use the generic Execute label")
		})
	}
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
