package ui

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

func IncreaseIndent(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	for i := range lines {
		lines[i] = "\t" + lines[i]
	}
	return strings.Join(lines, "\n")
}

// ToolDisplayName returns the short, human-facing name used by status lines
// and live activity labels. Unknown tools fall back to their protocol name.
func ToolDisplayName(name string) string {
	switch name {
	case "shell_run":
		return "shell"
	case "powershell_run":
		return "powershell"
	case "process_run":
		return "process"
	case "web_search":
		return "search web"
	case "fs_read_file":
		return "read"
	case "fs_write_file":
		return "write"
	case "fs_replace":
		return "replace"
	case "fs_list_dir":
		return "list"
	case "fs_stat":
		return "stat"
	case "fs_search":
		return "search"
	case "fs_largest":
		return "largest"
	case "fs_delete_file":
		return "delete"
	case "fs_delete_dir":
		return "delete dir"
	case "fs_mkdir":
		return "mkdir"
	case "fs_copy":
		return "copy"
	case "fs_move":
		return "move"
	case "fs_apply_patch":
		return "apply patch"
	case "fs_find_images", "fs_read_images":
		return "images"
	case "fs_find_media", "fs_read_media":
		return "media"
	case "http_download":
		return "download"
	case "todo_write":
		return "plan"
	case "request_user_input":
		return "ask"
	case "load_skill":
		return "skill"
	default:
		return name
	}
}

// ToolOperationLabel renders the live "running" label. It favors a short verb
// plus the most meaningful argument so the footer reads like a status line,
// not a protocol dump.
func ToolOperationLabel(name string, data []byte, width int) string {
	args := ToolOperationArgs(data)
	switch name {
	case "web_search":
		if query := OneLinePreview(ArgString(args, "query")); query != "" {
			return TruncateOperationStatus("search web  "+query, width)
		}
	case "shell_run":
		if command := ShellCommandPreview(ArgString(args, "command")); command != "" {
			return TruncateOperationStatus("shell  "+command, width)
		}
	case "powershell_run":
		if command := ShellCommandPreview(ArgString(args, "command")); command != "" {
			return TruncateOperationStatus("powershell  "+command, width)
		}
	case "process_run":
		if command := ProcessCommandPreview(args); command != "" {
			return TruncateOperationStatus("process  "+command, width)
		}
	case "fs_read_file":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("read  "+path, width)
		}
	case "fs_write_file":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("write  "+path, width)
		}
	case "fs_replace":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("replace  "+path, width)
		}
	case "fs_list_dir":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("list  "+path, width)
		}
	case "fs_stat":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("stat  "+path, width)
		}
	case "fs_search":
		query := OneLinePreview(ArgString(args, "query"))
		path := OneLinePreview(ArgString(args, "path"))
		switch {
		case query != "" && path != "":
			return TruncateOperationStatus("search  "+query+" in "+path, width)
		case query != "":
			return TruncateOperationStatus("search  "+query, width)
		case path != "":
			return TruncateOperationStatus("search  in "+path, width)
		}
	case "fs_largest":
		path := OneLinePreview(ArgString(args, "path"))
		kind := OneLinePreview(ArgString(args, "kind"))
		if kind == "" {
			kind = "files"
		}
		if path != "" {
			return TruncateOperationStatus("largest  "+kind+" in "+path, width)
		}
		return TruncateOperationStatus("largest  "+kind, width)
	case "fs_delete_file":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("delete  "+path, width)
		}
	case "fs_delete_dir":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("delete dir  "+path, width)
		}
	case "fs_mkdir":
		if path := OneLinePreview(ArgString(args, "path")); path != "" {
			return TruncateOperationStatus("mkdir  "+path, width)
		}
	case "fs_copy":
		source := OneLinePreview(ArgString(args, "source_path"))
		dest := OneLinePreview(ArgString(args, "dest_path"))
		if source != "" && dest != "" {
			return TruncateOperationStatus("copy  "+source+" → "+dest, width)
		}
	case "fs_move":
		source := OneLinePreview(ArgString(args, "source_path"))
		dest := OneLinePreview(ArgString(args, "dest_path"))
		if source != "" && dest != "" {
			return TruncateOperationStatus("move  "+source+" → "+dest, width)
		}
	case "fs_apply_patch":
		return TruncateOperationStatus("apply patch", width)
	case "todo_write":
		return TruncateOperationStatus("update plan", width)
	case "request_user_input":
		if question := OneLinePreview(ArgString(args, "question")); question != "" {
			return TruncateOperationStatus("ask  "+question, width)
		}
	case "load_skill":
		skillName := OneLinePreview(ArgString(args, "name"))
		file := OneLinePreview(ArgString(args, "file"))
		if skillName != "" && file != "" {
			return TruncateOperationStatus("skill  "+skillName+"  ("+file+")", width)
		}
		if skillName != "" {
			return TruncateOperationStatus("skill  "+skillName, width)
		}
		return TruncateOperationStatus("load skill", width)
	}
	if summary := ToolArgsSummary(args); summary != "" {
		return TruncateOperationStatus("run  "+name+"  ("+summary+")", width)
	}
	return TruncateOperationStatus("run  "+name, width)
}

func ToolOperationArgs(data []byte) map[string]any {
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		return nil
	}
	return args
}

func ToolArgsSummary(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	preferred := []string{"query", "command", "path", "source_path", "dest_path", "url", "repo", "repository", "file", "filename", "name"}
	parts := make([]string, 0, 3)
	seen := map[string]bool{}
	for _, key := range preferred {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			return strings.Join(parts, ", ")
		}
	}
	if len(parts) > 0 {
		return strings.Join(parts, ", ")
	}
	for key := range args {
		if appendToolArgSummaryPart(&parts, seen, args, key) && len(parts) >= 3 {
			break
		}
	}
	return strings.Join(parts, ", ")
}

func appendToolArgSummaryPart(parts *[]string, seen map[string]bool, args map[string]any, key string) bool {
	if seen[key] {
		return false
	}
	value := OneLinePreview(ArgString(args, key))
	if value == "" {
		return false
	}
	seen[key] = true
	*parts = append(*parts, key+"="+value)
	return true
}

func ArgString(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	value, ok := args[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case float64, bool:
		return fmt.Sprint(v)
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			s := OneLinePreview(fmt.Sprint(item))
			if s != "" {
				values = append(values, s)
			}
		}
		return strings.Join(values, ",")
	default:
		return ""
	}
}

func OneLinePreview(s string) string {
	s = strings.ReplaceAll(s, "\r", "\n")
	s = FirstLine(strings.TrimSpace(s))
	return strings.Join(strings.Fields(s), " ")
}

func FirstLine(s string) string {
	first, _, _ := strings.Cut(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return first
}

// ShellCommandPreview returns a compact one-line preview of a shell command.
// It skips leading blank lines and comment lines (lines whose first non-space
// character is '#') because models often prepend a '#' comment to narrate
// intent; without skipping, the preview would show only the comment and hide
// the command that actually runs. If every line is blank or a comment, it
// falls back to the first line so the preview is never empty.
func ShellCommandPreview(s string) string {
	normalized := strings.ReplaceAll(s, "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return OneLinePreview(line)
	}
	return OneLinePreview(s)
}

// ProcessCommandPreview renders a direct process invocation without implying
// that the displayed quoting is interpreted by a shell.
func ProcessCommandPreview(args map[string]any) string {
	program := ArgString(args, "program")
	if program == "" {
		return ""
	}
	parts := []string{quoteProcessPreviewArg(program)}
	if values, ok := args["args"].([]any); ok {
		for _, value := range values {
			arg, ok := value.(string)
			if !ok {
				continue
			}
			parts = append(parts, quoteProcessPreviewArg(arg))
		}
	}
	// Arguments are already quoted; collapsing whitespace here changes their
	// meaning (including spaces inside filenames and script payloads).
	return strings.Join(parts, " ")
}

func quoteProcessPreviewArg(arg string) string {
	if arg != "" && !strings.ContainsAny(arg, " \t\r\n\"'") {
		return arg
	}
	return strconv.Quote(arg)
}

func TruncateOperationStatus(s string, width int) string {
	s = strings.TrimSpace(s)
	if width <= 0 || width > 120 {
		width = 120
	}
	if ansi.StringWidth(s) <= width {
		return s
	}
	if width <= 3 {
		return ansi.Truncate(s, width, "")
	}
	return ansi.Truncate(s, width, "...")
}

// if the input is whitespace only, make it empty.
func RemoveWhitespace(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}
