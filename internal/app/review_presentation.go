package app

import (
	"strings"

	"github.com/panjie/mods/internal/approval"
)

type reviewPresentation struct {
	tone     interactionTone
	toneText string
	headline string
	rows     []interactionRow
}

func formatReviewPresentationWithIntent(name string, args []byte, assessment approval.CommandAssessment, scope Scope, intent AccessIntent) reviewPresentation {
	parsed := ToolOperationArgs(args)
	result := reviewPresentation{tone: interactionToneWarning, toneText: "Warning"}
	switch name {
	case "fs_delete_file":
		result.tone, result.toneText, result.headline = interactionToneDanger, "Danger", "Delete a file"
		result.rows = []interactionRow{{Label: "Target", Value: ArgString(parsed, "path")}}
	case "fs_delete_dir":
		result.tone, result.toneText, result.headline = interactionToneDanger, "Danger", "Delete a directory"
		result.rows = []interactionRow{{Label: "Target", Value: ArgString(parsed, "path")}}
	case "fs_write_file":
		result.headline = writeTargetMode(ArgString(parsed, "path"), scope)
		result.rows = []interactionRow{{Label: "Target", Value: ArgString(parsed, "path")}}
	case "fs_replace":
		result.headline = "Replace text in a file"
		result.rows = []interactionRow{{Label: "Target", Value: ArgString(parsed, "path")}}
	case "fs_mkdir":
		result.headline = "Create a directory"
		result.rows = []interactionRow{{Label: "Target", Value: ArgString(parsed, "path")}}
	case "fs_copy":
		result.headline = "Copy files or directories"
		result.rows = []interactionRow{{Label: "Source", Value: ArgString(parsed, "source_path")}, {Label: "Target", Value: ArgString(parsed, "dest_path")}}
	case "fs_move":
		result.headline = "Move or rename files"
		result.rows = []interactionRow{{Label: "Source", Value: ArgString(parsed, "source_path")}, {Label: "Target", Value: ArgString(parsed, "dest_path")}}
	case "fs_apply_patch":
		result.headline = "Apply changes to local files"
		result.rows = []interactionRow{{Label: "Patch", Value: patchSummary(ArgString(parsed, "patch"))}}
	case "fs_read_file", "fs_list_dir", "fs_stat", "fs_search", "fs_largest":
		result.tone, result.toneText, result.headline = interactionToneInfo, "Info", "Read local files"
		result.rows = []interactionRow{{Label: "Target", Value: readReviewTarget(parsed, scope, intent)}}
	case "shell_run", "powershell_run":
		command := ArgString(parsed, "command")
		risk := shellRiskLevel(assessment, scope)
		result.tone, result.toneText = toneForShellRisk(risk, command)
		result.headline = shellRiskHeadline(risk)
		result.rows = commandReviewRows(command, assessment, risk)
	case "process_run":
		risk := shellRiskLevel(assessment, scope)
		command := ProcessCommandPreview(parsed)
		result.tone, result.toneText = toneForShellRisk(risk, command)
		result.headline = shellRiskHeadline(risk)
		result.rows = commandReviewRows(command, assessment, risk)
		if cwd := ArgString(parsed, "cwd"); cwd != "" && cwd != scope.Value {
			result.rows = append(result.rows, interactionRow{Label: "Working dir", Value: cwd})
		}
	default:
		result.headline = "Run " + name
		if summary := ToolArgsSummary(parsed); summary != "" {
			result.rows = []interactionRow{{Label: "Details", Value: summary}}
		}
	}
	if origins := summarizeRemoteOrigins(intent.AllRemoteOrigins()); origins != "" && name != "shell_run" && name != "powershell_run" && name != "process_run" {
		result.rows = append(result.rows, interactionRow{Label: "Remote", Value: origins})
	}
	if result.headline == "" {
		result.headline = "This operation requires approval"
	}
	if intent.HasAccess() && intent.DominantClass() == AccessRead && result.tone == interactionToneWarning {
		result.tone, result.toneText = interactionToneInfo, "Info"
	}
	return result
}

func commandReviewRows(command string, assessment approval.CommandAssessment, risk string) []interactionRow {
	rows := []interactionRow{{Label: "Command", Value: redactRemoteURLsForDisplay(command)}}
	if target := commandReviewTarget(assessment, risk); target != "" {
		rows = append(rows, interactionRow{Label: "Target", Value: target})
	}
	if origins := summarizeRemoteOrigins(assessment.RemoteOrigins); origins != "" {
		rows = append(rows, interactionRow{Label: "Remote", Value: origins})
	}
	if len(assessment.UnresolvedRemoteTargets) > 0 {
		rows = append(rows, interactionRow{Label: "Remote", Value: "Unknown"})
	}
	return rows
}

func commandReviewTarget(assessment approval.CommandAssessment, risk string) string {
	dynamic := summarizeAffectedDirs(pathShapedDynamicTargets(assessment.DynamicTargets))
	known := summarizeAffectedDirs(assessment.KnownDirs)
	switch {
	case dynamic != "" && known != "":
		return dynamic + " · known: " + known
	case dynamic != "":
		return dynamic
	case known != "":
		return known
	case shellRiskLocationUnknown(risk):
		return "Unknown"
	default:
		return ""
	}
}

// pathShapedDynamicTargets drops dynamic targets that cannot be a filesystem
// path (no path separators, cmd-var syntax, or leading variable reference).
// Non-path fragments — for example elisp passed to an external program's
// --eval — must not be presented to the user as a review "Target". A fragment
// that merely embeds a displayed reference (a header value such as
// "PRIVATE-TOKEN: $env:TOKEN") is dropped as a duplicate of that reference,
// while one without a counterpart stays so the target never goes unshown.
func pathShapedDynamicTargets(targets []string) []string {
	if len(targets) == 0 {
		return nil
	}
	var refs, composites []string
	for _, target := range targets {
		switch {
		case strings.ContainsAny(target, `/\%`) || strings.HasPrefix(strings.TrimSpace(target), "$"):
			refs = append(refs, target)
		case strings.Contains(target, "$"):
			composites = append(composites, target)
		}
	}
	visible := append([]string(nil), refs...)
	for _, composite := range composites {
		if !embedsTargetOf(composite, refs) {
			visible = append(visible, composite)
		}
	}
	return visible
}

// embedsTargetOf reports whether target contains one of the displayed
// variable references verbatim.
func embedsTargetOf(target string, refs []string) bool {
	for _, ref := range refs {
		if ref != target && strings.Contains(target, ref) {
			return true
		}
	}
	return false
}

func toneForShellRisk(risk, command string) (interactionTone, string) {
	if strings.Contains(command, "sudo") || risk == "dynamic mutation" {
		return interactionToneDanger, "Danger"
	}
	if risk == "local mutation" || risk == "unknown" || shellRiskLocationUnknown(risk) {
		return interactionToneWarning, "Warning"
	}
	return interactionToneInfo, "Info"
}

func shellRiskHeadline(risk string) string {
	switch risk {
	case "dynamic mutation":
		return "Modify a dynamic target"
	case "dynamic read":
		return "Read a dynamic target"
	case "local mutation":
		return "Modify local files"
	case "remote mutation":
		return "Modify a remote resource"
	case "unknown remote mutation":
		return "Modify an unknown remote resource"
	case "unknown-location mutation":
		return "Modify an unknown target"
	case "unknown effect and location":
		return "Run with unknown effects"
	case "read-only":
		return "Run a read-only command"
	default:
		return "Run with unknown effects"
	}
}
