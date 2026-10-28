package app

import (
	"fmt"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/panjie/mods/internal/approval"
)

type reviewPresentation struct {
	tone     interactionTone
	toneText string
	headline string
	rows     []interactionRow
}

func formatReviewPresentationWithIntent(name string, args []byte, scope Scope, intent AccessIntent) reviewPresentation {
	parsed := ToolOperationArgs(args)
	result := reviewPresentation{tone: interactionToneWarning, toneText: "Warning"}
	switch name {
	case "http_download":
		result.headline = "Download files"
		overwrite, _ := parsed["overwrite"].(bool)
		result.rows = []interactionRow{{Label: "Overwrite", Value: fmt.Sprint(overwrite)}}
		if files, ok := parsed["files"].([]any); ok {
			for i, file := range files {
				if item, ok := file.(map[string]any); ok {
					result.rows = append(result.rows, interactionRow{Label: fmt.Sprintf("File %d", i+1), Value: ArgString(item, "path")}, interactionRow{Label: "Source", Value: downloadReviewURL(ArgString(item, "url"))})
				}
			}
		}
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
		result.headline = "Run command"
		result.rows = []interactionRow{{Label: "Model declaration", Value: ArgString(parsed, "effect")}, {Label: "Source", Value: numberedReviewSource(redactRemoteURLsForDisplay(command))}}
		interpreter := "PowerShell"
		if name == "shell_run" && runtime.GOOS != "windows" {
			interpreter = "POSIX shell"
		}
		result.rows = append(result.rows, interactionRow{Label: "Interpreter", Value: interpreter})
		result.rows = append(result.rows, interactionRow{Label: "Working dir", Value: reviewWorkingDir(parsed, scope)})
	case "process_run":
		result.headline = "Run process"
		result.rows = []interactionRow{
			{Label: "Model declaration", Value: ArgString(parsed, "effect")},
			{Label: "Program", Value: ArgString(parsed, "program")},
			{Label: "Argv", Value: processReviewArgv(parsed)},
			{Label: "Working dir", Value: reviewWorkingDir(parsed, scope)},
		}
	default:
		result.headline = "Run " + name
		if summary := ToolArgsSummary(parsed); summary != "" {
			result.rows = []interactionRow{{Label: "Details", Value: summary}}
		}
	}
	if origins := strings.Join(approval.NormalizeRemoteOrigins(intent.AllRemoteOrigins()), ", "); origins != "" && name != "shell_run" && name != "powershell_run" && name != "process_run" {
		result.rows = append(result.rows, interactionRow{Label: "Remote", Value: origins})
	}
	if result.headline == "" {
		result.headline = "This operation requires approval"
	}
	if elevated, _ := parsed["elevated"].(bool); elevated && (name == "process_run" || name == "shell_run" || name == "powershell_run") {
		result.rows = append(result.rows, interactionRow{Label: "Elevation", Value: "Windows UAC administrator privileges (this command only)"})
	}
	if intent.HasAccess() && intent.DominantClass() == AccessRead && result.tone == interactionToneWarning {
		result.tone, result.toneText = interactionToneInfo, "Info"
	}
	if intent.DominantClass() == AccessWrite {
		commandTool := name == "shell_run" || name == "powershell_run" || name == "process_run"
		if !intent.HasCompleteWriteTargets() && !commandTool {
			result.rows = append(result.rows, interactionRow{Label: "Coverage", Value: "Write-target coverage is incomplete; approval applies to this call only."})
		}
		for _, group := range intent.Groups() {
			if group.Class == AccessWrite && len(group.Dirs) > 0 && name != "shell_run" && name != "powershell_run" && name != "process_run" {
				result.rows = append(result.rows, interactionRow{Label: "Write scope", Value: strings.Join(group.Dirs, "\n")})
			}
		}
	}

	return result
}

func downloadReviewURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "[invalid URL]"
	}
	u.User, u.Fragment = nil, ""
	if u.RawQuery != "" {
		u.RawQuery = "[redacted query]"
	}
	return u.String()
}

func reviewWorkingDir(parsed map[string]any, scope Scope) string {
	if cwd := ArgString(parsed, "cwd"); cwd != "" {
		return cwd
	}
	return scope.Value
}

func processReviewArgv(parsed map[string]any) string {
	argv := []string{ArgString(parsed, "program")}
	if values, ok := parsed["args"].([]any); ok {
		for _, value := range values {
			arg, ok := value.(string)
			if !ok {
				continue
			}
			argv = append(argv, arg)
		}
	}
	return quotedReviewArgv(argv)
}

func quotedReviewArgv(argv []string) string {
	lines := make([]string, len(argv))
	for i, arg := range argv {
		lines[i] = strconv.Itoa(i) + ": " + strconv.Quote(redactRemoteURLsForDisplay(arg))
	}
	return strings.Join(lines, "\n")
}

func numberedReviewSource(source string) string {
	source = strings.ReplaceAll(source, "\r\n", "\n")
	lines := strings.Split(source, "\n")
	for i := range lines {
		lines[i] = strconv.Itoa(i+1) + " | " + lines[i]
	}
	return strings.Join(lines, "\n")
}

// Keep affected resources ahead of executable details without dropping any row.
func reviewOrderedRows(rows []interactionRow) []interactionRow {
	out := slices.Clone(rows)
	command := false
	// Batch downloads pair each File N with its following Source. Keep those
	// pairs adjacent so a source cannot appear to belong to another target.
	for _, row := range rows {
		command = command || row.Label == "Model declaration"
		if strings.HasPrefix(row.Label, "File ") {
			return out
		}
	}
	priority := func(label string) int {
		if command {
			switch label {
			case "Model declaration":
				return 0
			case "Working dir":
				return 1
			case "Source", "Argv":
				return 3
			default:
				return 2
			}
		}
		switch label {
		case "Target", "Remote", "Write target", "Write target (unresolved)":
			return 0
		case "Source", "Read source", "Read source (unresolved)", "Working dir", "Overwrite":
			return 1
		case "Interpreter", "Program", "Argv":
			return 2
		case "Command", "Patch", "Details":
			return 3
		default:
			return 2
		}
	}
	slices.SortStableFunc(out, func(a, b interactionRow) int { return priority(a.Label) - priority(b.Label) })
	return out
}

// Only reuse a displayed target when its full text exactly matches the grant.
// A file target or a narrower directory must never hide a broader session grant.
func sessionRuleLabel(rules []Rule, rows []interactionRow) string {
	var parts []string
	for _, rule := range rules {
		label := rule.String()
		if rule.Type == approval.DirAllow && rule.Mode == AccessWrite {
			target := strings.Join(rule.Paths, ", ")
			label = "Write dirs: " + target + " (including subdirectories)"
			for _, row := range rows {
				if row.Label == "Target" && row.Value == target && target != "" {
					label = "Write above directories and subdirectories"
					break
				}
			}
		} else if rule.Type == approval.RemoteAllow {
			label += " (exact origins)"
		}
		parts = append(parts, label)
	}
	return strings.Join(parts, "; ") + "; this conversation only"
}
