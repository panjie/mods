package app

import (
	"fmt"
	"net/url"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/commandanalysis"
)

type reviewPresentation struct {
	tone     interactionTone
	toneText string
	headline string
	rows     []interactionRow
}

func formatReviewPresentationWithIntent(name string, args []byte, assessment commandanalysis.CommandAssessment, scope Scope, intent AccessIntent) reviewPresentation {
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
		risk := shellRiskLevel(assessment, scope)
		result.tone, result.toneText = toneForShellRisk(risk, command)
		result.headline = shellRiskHeadline(risk)
		result.rows = commandReviewRows(command, assessment, risk)
		interpreter := "PowerShell"
		if name == "shell_run" && runtime.GOOS != "windows" {
			interpreter = "POSIX shell"
		}
		result.rows = append(result.rows, interactionRow{Label: "Interpreter", Value: interpreter})
		result.rows = append(result.rows, interactionRow{Label: "Working dir", Value: reviewWorkingDir(parsed, scope)})
	case "process_run":
		risk := shellRiskLevel(assessment, scope)
		command := ProcessCommandPreview(parsed)
		result.tone, result.toneText = toneForShellRisk(risk, command)
		result.headline = shellRiskHeadline(risk)
		// Native argv has no shell-source coordinate system. Do not apply any
		// nested payload offsets to the lossy command preview.
		displayAssessment := assessment.Clone()
		if displayAssessment.Structure != nil {
			for i := range displayAssessment.Structure.Findings {
				displayAssessment.Structure.Findings[i].Located = false
			}
		}
		result.rows = commandReviewRows(command, displayAssessment, risk)[1:]
		result.rows = append(result.rows,
			interactionRow{Label: "Program", Value: ArgString(parsed, "program")},
			interactionRow{Label: "Argv", Value: processReviewArgv(parsed)},
			interactionRow{Label: "Working dir", Value: reviewWorkingDir(parsed, scope)},
		)
	default:
		result.headline = "Run " + name
		if summary := ToolArgsSummary(parsed); summary != "" {
			result.rows = []interactionRow{{Label: "Details", Value: summary}}
		}
	}
	if origins := strings.Join(commandanalysis.NormalizeRemoteOrigins(intent.AllRemoteOrigins()), ", "); origins != "" && name != "shell_run" && name != "powershell_run" && name != "process_run" {
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

func commandReviewRows(command string, assessment commandanalysis.CommandAssessment, risk string) (rows []interactionRow) {
	// Presentation-only deduplication: retain every target and node identity
	// in the assessment used by authorization and revalidation.
	defer func() {
		seen := map[interactionRow]bool{}
		unique := make([]interactionRow, 0, len(rows))
		for _, row := range rows {
			if !seen[row] {
				unique = append(unique, row)
				seen[row] = true
			}
		}
		rows = unique
	}()
	rows = []interactionRow{{Label: "Source", Value: numberedReviewSource(redactRemoteURLsForDisplay(command))}}
	for _, finding := range commandStructureFindings(command, assessment.Structure) {
		rows = append(rows, interactionRow{Label: "Analysis limit", Value: finding})
	}
	for _, diagnostic := range assessment.Diagnostics {
		rows = append(rows, interactionRow{Label: "Analysis", Value: redactRemoteURLsForDisplay(diagnostic)})
	}
	if len(assessment.Targets) > 0 || len(assessment.UnresolvedRemoteTargets) > 0 {
		if recognized := recognizedCommandOperations(assessment.Targets); recognized != "" {
			rows = append(rows, interactionRow{Label: "Recognized", Value: recognized})
		}
		type displayTarget struct {
			kind commandanalysis.TargetKind
			path string
		}
		proven := map[displayTarget]bool{}
		for _, target := range assessment.Targets {
			if target.Role != commandanalysis.TargetCandidate && target.State == commandanalysis.TargetResolved && target.Path != "" {
				proven[displayTarget{target.Kind, target.Path}] = true
			}
		}
		for _, target := range assessment.Targets {
			if target.Role == commandanalysis.TargetCandidate && target.State == commandanalysis.TargetResolved && proven[displayTarget{target.Kind, target.Path}] {
				continue
			}
			label := "Candidate"
			switch target.Role {
			case commandanalysis.TargetRead:
				label = "Read source"
			case commandanalysis.TargetWrite:
				label = "Write target"
			}
			value := target.Path
			if target.State != commandanalysis.TargetResolved {
				value = target.Raw
				label += " (unresolved)"
			}
			if value == "" {
				value = target.Raw
			}
			if target.LexicalPath != "" && target.LexicalPath != target.Path && target.State == commandanalysis.TargetResolved {
				value = target.LexicalPath + " → " + target.Path
			}
			rows = append(rows, interactionRow{Label: label, Value: redactRemoteURLsForDisplay(value)})
		}
		if assessment.Effect == commandanalysis.EffectUnknown {
			rows = append(rows, interactionRow{Label: "Effect", Value: "Cannot establish whether additional side effects are present."})
		} else if assessment.Effect == commandanalysis.EffectWrite && assessment.WriteCoverage != approval.CoverageComplete {
			rows = append(rows, interactionRow{Label: "Coverage", Value: "Write-target coverage is incomplete; approval applies to this call only."})
		}
		for _, target := range assessment.UnresolvedRemoteTargets {
			rows = append(rows, interactionRow{Label: "Remote (unresolved)", Value: redactRemoteURLsForDisplay(target)})
		}
		return rows
	}
	if len(assessment.Diagnostics) > 0 {
		for _, expression := range assessment.DynamicTargets {
			rows = append(rows, interactionRow{Label: "Unresolved expression", Value: redactRemoteURLsForDisplay(expression)})
		}
		assessment.DynamicTargets = nil
	}
	if target := commandReviewTarget(assessment, risk); target != "" {
		rows = append(rows, interactionRow{Label: "Target", Value: target})
	}
	if origins := strings.Join(commandanalysis.NormalizeRemoteOrigins(assessment.RemoteOrigins), ", "); origins != "" {
		rows = append(rows, interactionRow{Label: "Remote", Value: origins})
	}
	if len(assessment.UnresolvedRemoteTargets) > 0 {
		rows = append(rows, interactionRow{Label: "Remote", Value: "Unknown"})
	}
	if assessment.Effect == commandanalysis.EffectUnknown {
		rows = append(rows, interactionRow{Label: "Effect", Value: "Cannot establish whether additional side effects are present."})
	} else if assessment.Effect == commandanalysis.EffectWrite && assessment.WriteCoverage != approval.CoverageComplete {
		rows = append(rows, interactionRow{Label: "Coverage", Value: "Write-target coverage is incomplete; approval applies to this call only."})
	}
	return rows
}

func recognizedCommandOperations(targets []commandanalysis.Target) string {
	seen := map[string]bool{}
	var operations []string
	for _, target := range targets {
		if target.Role == commandanalysis.TargetCandidate {
			continue
		}
		operation := "write"
		if target.Role == commandanalysis.TargetRead || target.Operation == commandanalysis.OperationRead {
			operation = "read"
		}
		kind := "filesystem"
		switch target.Kind {
		case commandanalysis.TargetOrigin:
			kind = "remote resource"
		case commandanalysis.TargetProvider:
			kind = "provider target"
		}
		value := operation + " " + kind
		if !seen[value] {
			seen[value] = true
			operations = append(operations, value)
		}
	}
	return strings.Join(operations, "; ")
}

func commandStructureFindings(command string, structure *commandanalysis.CommandStructure) []string {
	if structure == nil {
		return nil
	}
	findings := make([]string, 0, len(structure.Findings))
	for _, finding := range structure.Findings {
		label := map[commandanalysis.StructureCode]string{
			commandanalysis.StructureControlFlow:       "control flow",
			commandanalysis.StructureState:             "local state assignment",
			commandanalysis.StructureScriptBlock:       "script block",
			commandanalysis.StructureHost:              "nested interpreter host",
			commandanalysis.StructurePayload:           "inline interpreter payload",
			commandanalysis.StructureUnknownInvocation: "unmodeled invocation",
			commandanalysis.StructureMethod:            "unmodeled method expression",
		}[finding.Code]
		if label == "" {
			label = "unmodeled structure"
		}
		location := "command level"
		if line, ok := commandFindingLine(command, structure.OffsetUnit, finding); ok {
			location = fmt.Sprintf("line %d", line)
		}
		if finding.NodeID > 0 {
			location += fmt.Sprintf(", node %d", finding.NodeID)
		}
		findings = append(findings, label+" ["+location+"]")
	}
	if structure.Truncated {
		findings = append(findings, "Additional structure findings omitted (diagnostic limit).")
	}
	return findings
}

func commandFindingLine(command, unit string, finding commandanalysis.StructureFinding) (int, bool) {
	if !finding.Located || finding.Start < 0 || finding.End < finding.Start {
		return 0, false
	}
	prefix := command
	if unit == "utf16" {
		units := utf16.Encode([]rune(command))
		if finding.End > len(units) {
			return 0, false
		}
		for _, offset := range []int{finding.Start, finding.End} {
			if offset > 0 && offset < len(units) && units[offset-1] >= 0xd800 && units[offset-1] <= 0xdbff && units[offset] >= 0xdc00 && units[offset] <= 0xdfff {
				return 0, false
			}
		}
		prefix = string(utf16.Decode(units[:finding.Start]))
	} else {
		if unit != "byte" || finding.End > len(command) {
			return 0, false
		}
		if !utf8.ValidString(command[:finding.Start]) || !utf8.ValidString(command[:finding.End]) {
			return 0, false
		}
		prefix = command[:finding.Start]
	}
	return 1 + strings.Count(prefix, "\n"), true
}

func commandReviewTarget(assessment commandanalysis.CommandAssessment, risk string) string {
	// Approval content must be complete. Compact summaries are only for status
	// output; the review panel paginates these full target lists.
	dynamic := strings.Join(pathShapedDynamicTargets(assessment.DynamicTargets), ", ")
	providers := strings.Join(assessment.ProviderWriteTargets, ", ")
	known := strings.Join(assessment.KnownDirs, ", ")
	var targets []string
	if dynamic != "" {
		targets = append(targets, dynamic)
	}
	if providers != "" {
		targets = append(targets, providers)
	}
	if known != "" {
		if len(targets) > 0 {
			known = "known: " + known
		}
		targets = append(targets, known)
	}
	if len(targets) > 0 {
		return strings.Join(targets, " · ")
	}
	if shellRiskLocationUnknown(risk) {
		return "Unknown"
	}
	return ""
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
	if risk == "local mutation" || risk == "remote mutation" || risk == "provider mutation" || risk == "unknown" || shellRiskLocationUnknown(risk) {
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
	case "provider mutation":
		return "Modify a PowerShell provider target"
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

// Keep affected resources ahead of executable details without dropping any row.
func reviewOrderedRows(rows []interactionRow) []interactionRow {
	out := slices.Clone(rows)
	// Batch downloads pair each File N with its following Source. Keep those
	// pairs adjacent so a source cannot appear to belong to another target.
	for _, row := range rows {
		if strings.HasPrefix(row.Label, "File ") {
			return out
		}
	}
	priority := func(label string) int {
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
