package app

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

type patchFileChange struct {
	path    string
	added   int
	removed int
}

func formatReviewSummary(name string, args []byte, analysis shellCommandAnalysis, scope Scope) string {
	return formatReviewSummaryWithIntent(name, args, analysis, scope, AccessIntent{})
}

func formatReviewSummaryWithIntent(name string, args []byte, analysis shellCommandAnalysis, scope Scope, intent AccessIntent) string {
	parsed := ToolOperationArgs(args)
	switch name {
	case "fs_read_file", "fs_list_dir", "fs_stat", "fs_search":
		return fmt.Sprintf("Target: %s - external read", OneLinePreview(readReviewTarget(parsed, scope, intent)))
	case "fs_write_file":
		path := ArgString(parsed, "path")
		content := ArgString(parsed, "content")
		mode := writeTargetMode(path, scope)
		return fmt.Sprintf("Target: %s - %s - %d bytes", OneLinePreview(path), mode, len(content))
	case "fs_apply_patch":
		return patchSummary(ArgString(parsed, "patch"))
	case "shell_run", "powershell_run":
		command := ArgString(parsed, "command")
		return shellRiskSummary(command, analysis, scope)
	default:
		return ""
	}
}

func readReviewTarget(parsed map[string]any, scope Scope, intent AccessIntent) string {
	if len(intent.Dirs) == 1 {
		return intent.Dirs[0]
	}
	if len(intent.Dirs) > 1 {
		return summarizeAffectedDirs(intent.Dirs)
	}
	path := ArgString(parsed, "path")
	if path == "" {
		return ""
	}
	target := pathutil.NormalizePath(path, pathutil.DefaultOptions(scope.Value, pathutil.FlavorPOSIX))
	if info, err := os.Stat(target); err == nil {
		if info.IsDir() {
			return target
		}
		return pathutil.ParentDir(target)
	}
	return pathutil.ParentDir(target)
}

func writeTargetMode(path string, scope Scope) string {
	if path == "" {
		return "unknown target"
	}
	target := pathutil.NormalizePath(path, pathutil.DefaultOptions(scope.Value, pathutil.FlavorPOSIX))
	info, err := os.Stat(target)
	switch {
	case err == nil && info.IsDir():
		return "would replace directory path"
	case err == nil:
		return "overwrites existing file"
	case os.IsNotExist(err):
		return "creates new file"
	default:
		return "may overwrite"
	}
}

func shellRiskSummary(command string, analysis shellCommandAnalysis, scope Scope) string {
	risk := shellRiskLevel(analysis, scope)
	dirs := summarizeAffectedDirs(analysis.AffectedDirs)
	if dirs == "" {
		return fmt.Sprintf("Risk: %s - %s", risk, ShellCommandPreview(command))
	}
	return fmt.Sprintf("Risk: %s - affects %s", risk, dirs)
}

func shellRiskLevel(analysis shellCommandAnalysis, scope Scope) string {
	if !analysis.NeedsReview {
		for _, dir := range analysis.AffectedDirs {
			if !pathWithinScope(dir, scope) {
				return "external read"
			}
		}
		return "read-only"
	}
	if len(analysis.AffectedDirs) == 0 {
		return "unknown"
	}
	for _, dir := range analysis.AffectedDirs {
		if !pathWithinScope(dir, scope) {
			return "external mutation"
		}
	}
	return "workspace mutation"
}

func pathWithinScope(path string, scope Scope) bool {
	if path == "" {
		return false
	}
	return pathutil.Location(path, scope.Value, nil) == pathutil.LocationWorkspace
}

func summarizeAffectedDirs(dirs []string) string {
	if len(dirs) == 0 {
		return ""
	}
	const maxDirs = 3
	parts := make([]string, 0, min(len(dirs), maxDirs))
	for i, dir := range dirs {
		if i >= maxDirs {
			break
		}
		parts = append(parts, OneLinePreview(dir))
	}
	if len(dirs) > maxDirs {
		parts = append(parts, "+"+strconv.Itoa(len(dirs)-maxDirs)+" more")
	}
	return strings.Join(parts, ", ")
}

func patchSummary(patch string) string {
	changes := parsePatchSummary(patch)
	if len(changes) == 0 {
		return "Patch summary unavailable."
	}
	const maxFiles = 3
	parts := make([]string, 0, min(len(changes), maxFiles))
	for i, change := range changes {
		if i >= maxFiles {
			break
		}
		parts = append(parts, fmt.Sprintf("%s (+%d -%d)", change.path, change.added, change.removed))
	}
	if len(changes) > maxFiles {
		parts = append(parts, "+"+strconv.Itoa(len(changes)-maxFiles)+" more")
	}
	return "Patch: " + strings.Join(parts, "; ")
}

func parsePatchSummary(patch string) []patchFileChange {
	var changes []patchFileChange
	var current *patchFileChange
	flush := func() {
		if current == nil {
			return
		}
		if current.path != "" || current.added > 0 || current.removed > 0 {
			if current.path == "" {
				current.path = "(unknown)"
			}
			changes = append(changes, *current)
		}
		current = nil
	}
	for _, line := range strings.Split(strings.ReplaceAll(patch, "\r\n", "\n"), "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			flush()
			current = &patchFileChange{}
			fields := strings.Fields(line)
			if len(fields) >= 4 {
				current.path = cleanPatchPath(fields[3])
			}
		case strings.HasPrefix(line, "+++ "):
			if current == nil {
				current = &patchFileChange{}
			}
			path := cleanPatchPath(strings.TrimSpace(strings.TrimPrefix(line, "+++ ")))
			if path != "" && path != "/dev/null" {
				current.path = path
			}
		case strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++"):
			if current == nil {
				current = &patchFileChange{}
			}
			current.added++
		case strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---"):
			if current == nil {
				current = &patchFileChange{}
			}
			current.removed++
		}
	}
	flush()
	return changes
}

func cleanPatchPath(path string) string {
	path, _, _ = strings.Cut(path, "\t")
	path = strings.Trim(path, `"`)
	path = strings.TrimSpace(path)
	if path == "/dev/null" {
		return path
	}
	if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
		path = path[2:]
	}
	return OneLinePreview(path)
}
