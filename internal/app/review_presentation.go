package app

import (
	"encoding/json"
	"fmt"
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

func formatReviewPresentationWithIntent(name string, args []byte, analysis shellCommandAnalysis, scope Scope, intent AccessIntent) reviewPresentation {
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
		result.tone, result.toneText, result.headline = interactionToneInfo, "Info", "Read data outside the workspace"
		result.rows = []interactionRow{{Label: "Target", Value: readReviewTarget(parsed, scope, intent)}}
	case "shell_run", "powershell_run":
		command := ArgString(parsed, "command")
		risk := shellRiskLevel(analysis, scope)
		result.tone, result.toneText = toneForShellRisk(risk, command)
		result.headline = shellRiskHeadline(risk)
		result.rows = append(result.rows, interactionRow{Label: "Command", Value: command})
		if dirs := summarizeAffectedDirs(analysis.AffectedDirs); dirs != "" {
			result.rows = append(result.rows, interactionRow{Label: "Scope", Value: dirs})
		}
		if dynamic := summarizeAffectedDirs(analysis.UnresolvedPaths); dynamic != "" {
			result.rows = append(result.rows, interactionRow{Label: dynamicTargetLabel(analysis), Value: dynamic})
		}
		if reason := strings.TrimSpace(analysis.Reason); reason != "" {
			result.rows = append(result.rows, interactionRow{Label: "Reason", Value: reason})
		}
		result.rows = appendReviewabilityRows(result.rows, analysis)
	case "process_run":
		risk := shellRiskLevel(analysis, scope)
		result.tone, result.toneText = toneForShellRisk(risk, ProcessCommandPreview(parsed))
		result.headline = shellRiskHeadline(risk)
		result.rows = []interactionRow{
			{Label: "Program", Value: ArgString(parsed, "program")},
			{Label: "Arguments", Value: processArgsForReview(parsed)},
			{Label: "Working dir", Value: processCwdForReview(parsed, scope)},
		}
		if dirs := summarizeAffectedDirs(analysis.AffectedDirs); dirs != "" {
			result.rows = append(result.rows, interactionRow{Label: "Scope", Value: dirs})
		}
		if dynamic := summarizeAffectedDirs(analysis.UnresolvedPaths); dynamic != "" {
			result.rows = append(result.rows, interactionRow{Label: dynamicTargetLabel(analysis), Value: dynamic})
		}
		if reason := strings.TrimSpace(analysis.Reason); reason != "" {
			result.rows = append(result.rows, interactionRow{Label: "Reason", Value: reason})
		}
		result.rows = appendReviewabilityRows(result.rows, analysis)
	default:
		result.headline = formatReviewLabel(name, args)
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

func appendReviewabilityRows(rows []interactionRow, analysis shellCommandAnalysis) []interactionRow {
	reviewability := analysis.Reviewability
	if reviewability.Level == "" || reviewability.Level == approval.ReviewabilitySimple && !reviewability.ShouldCorrect && reviewability.RecommendedTool == "" {
		return rows
	}
	rows = append(rows, interactionRow{Label: "Reviewability", Value: string(reviewability.Level)})
	var shape []string
	if reviewability.TopLevelStatements > 1 {
		shape = append(shape, fmt.Sprintf("%d top-level actions", reviewability.TopLevelStatements))
	}
	if reviewability.PipelineCount > 0 {
		shape = append(shape, fmt.Sprintf("%d pipelines", reviewability.PipelineCount))
	}
	if len(shape) > 0 {
		rows = append(rows, interactionRow{Label: "Composition", Value: strings.Join(shape, ", ")})
	}
	if suggestion := reviewabilitySuggestion(reviewability); suggestion != "" {
		rows = append(rows, interactionRow{Label: "Suggestion", Value: suggestion})
	}
	return rows
}

func reviewabilitySuggestion(reviewability approval.CommandReviewability) string {
	if reviewability.RecommendedTool == "process_run" {
		return "Use process_run with literal arguments"
	}
	if containsReviewabilityReason(reviewability.Reasons, approval.ReviewabilityDynamicWriteTarget) {
		return "Resolve the path read-only, then write the literal absolute path"
	}
	if reviewability.ShouldCorrect {
		return "Split independent discovery, inspection, mutation, and verification steps"
	}
	return ""
}

func processArgsForReview(parsed map[string]any) string {
	values, _ := parsed["args"].([]any)
	if len(values) == 0 {
		return "(none)"
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "(invalid)"
	}
	return string(data)
}

func processCwdForReview(parsed map[string]any, scope Scope) string {
	if cwd := ArgString(parsed, "cwd"); cwd != "" {
		return cwd
	}
	return scope.Value
}

func toneForShellRisk(risk, command string) (interactionTone, string) {
	if strings.Contains(command, "sudo") || risk == "external mutation" || risk == "dynamic mutation" {
		return interactionToneDanger, "Danger"
	}
	if risk == "workspace mutation" || risk == "unknown" {
		return interactionToneWarning, "Warning"
	}
	return interactionToneInfo, "Info"
}

func dynamicTargetLabel(analysis shellCommandAnalysis) string {
	switch normalizeShellEffect(analysis).Effect {
	case shellEffectRead:
		return "Dynamic read target"
	case shellEffectWrite:
		return "Dynamic write target"
	default:
		return "Dynamic target"
	}
}

func shellRiskHeadline(risk string) string {
	switch risk {
	case "external mutation":
		return "Modify state outside the workspace"
	case "dynamic mutation":
		return "Modify a runtime-resolved target"
	case "dynamic read":
		return "Read from runtime-resolved paths"
	case "workspace mutation":
		return "Modify files in the workspace"
	case "external read":
		return "Read data outside the workspace"
	case "read-only":
		return "Run a read-only command"
	default:
		return "Run a command with unknown effects"
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
