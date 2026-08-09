package app

import (
	"encoding/json"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/secrets"
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
		result.headline = "Apply changes to workspace files"
		result.rows = []interactionRow{{Label: "Patch", Value: patchSummary(ArgString(parsed, "patch"))}}
	case "fs_read_file", "fs_list_dir", "fs_stat", "fs_search", "fs_largest":
		result.tone, result.toneText, result.headline = interactionToneInfo, "Info", "Read outside the workspace"
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
	if result.headline == "" {
		result.headline = "This operation requires approval"
	}
	if intent.HasAccess() && intent.DominantClass() == AccessRead && result.tone == interactionToneWarning {
		result.tone, result.toneText = interactionToneInfo, "Info"
	}
	return result
}

func commandReviewRows(command string, assessment approval.CommandAssessment, risk string) []interactionRow {
	rows := []interactionRow{{Label: "Command", Value: command}}
	if target := commandReviewTarget(assessment, risk); target != "" {
		rows = append(rows, interactionRow{Label: "Target", Value: target})
	}
	return rows
}

func commandReviewTarget(assessment approval.CommandAssessment, risk string) string {
	dynamic := summarizeAffectedDirs(assessment.DynamicTargets)
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

func toneForShellRisk(risk, command string) (interactionTone, string) {
	if strings.Contains(command, "sudo") || risk == "external mutation" || risk == "dynamic mutation" {
		return interactionToneDanger, "Danger"
	}
	if risk == "workspace mutation" || risk == "unknown" || shellRiskLocationUnknown(risk) {
		return interactionToneWarning, "Warning"
	}
	return interactionToneInfo, "Info"
}

func shellRiskHeadline(risk string) string {
	switch risk {
	case "external mutation":
		return "Modify outside the workspace"
	case "dynamic mutation":
		return "Modify a dynamic target"
	case "dynamic read":
		return "Read a dynamic target"
	case "workspace mutation":
		return "Modify workspace files"
	case "unknown-location mutation":
		return "Modify an unknown target"
	case "unknown effect and location":
		return "Run with unknown effects"
	case "external read":
		return "Read outside the workspace"
	case "read-only":
		return "Run a read-only command"
	default:
		return "Run with unknown effects"
	}
}

func secretReferenceTargets(data []byte) string {
	var root any
	if err := json.Unmarshal(data, &root); err != nil {
		return "protected argument"
	}
	var paths []string
	var walk func(any, string)
	walk = func(value any, path string) {
		switch value := value.(type) {
		case string:
			if secrets.IsRef(value) {
				paths = append(paths, path)
			}
		case map[string]any:
			for key, child := range value {
				walk(child, path+"/"+strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1"))
			}
		case []any:
			for _, child := range value {
				walk(child, path)
			}
		}
	}
	walk(root, "")
	if len(paths) == 0 {
		return "protected argument"
	}
	return strings.Join(paths, ", ")
}
