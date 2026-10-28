package ui

import (
	"errors"
	"fmt"

	"github.com/charmbracelet/x/ansi"
)

// exitCoder is implemented by errors that carry a process exit code, such as
// tools.ShellExitError. It avoids coupling this package to the tools package.
type exitCoder interface {
	ExitCode() int
}

// ToolResultStatus returns the unstyled one-line status text used by the live
// operation footer. It starts with a success/failure marker so callers can
// apply styling uniformly, then names the action and its most useful target.
func ToolResultStatus(name string, data []byte, err error, width int) string {
	if name == "" {
		return ""
	}
	marker := "\u2713"
	if err != nil {
		marker = "\u2717"
	}
	prefix := marker + " " + ToolDisplayName(name)
	summary := toolResultSummary(name, data)
	suffix := ""
	detail := ""
	if err == nil {
		if isShellTool(name) {
			suffix = "exit 0"
		}
	} else {
		var ec exitCoder
		if errors.As(err, &ec) {
			suffix = fmt.Sprintf("exit %d", ec.ExitCode())
		} else {
			detail = OneLinePreview(err.Error())
		}
	}
	return toolStatusLine(prefix, summary, suffix, detail, width)
}

const toolStatusSeparator = "  \u00b7 "

func toolStatusLine(prefix, summary, suffix, detail string, width int) string {
	tail := suffix
	if detail != "" {
		if tail != "" {
			tail += toolStatusSeparator + detail
		} else {
			tail = detail
		}
	}
	full := prefix
	if summary != "" {
		full += "  " + summary
	}
	if tail != "" {
		full += toolStatusSeparator + tail
	}
	if width <= 0 || displayWidth(full) <= width {
		return full
	}
	// Shrink the argument summary first; the exit status and failure reason
	// carry the information users need after a call completes.
	if summary != "" {
		if tail == "" {
			if available := width - displayWidth(prefix) - len("  "); available > 0 {
				return prefix + "  " + TruncateOperationStatus(summary, available)
			}
		} else {
			available := width - displayWidth(prefix) - len("  ") - displayWidth(toolStatusSeparator) - displayWidth(tail)
			if available >= minToolSummaryWidth {
				return prefix + "  " + TruncateOperationStatus(summary, available) + toolStatusSeparator + tail
			}
		}
	}
	if detail != "" {
		base := prefix
		if suffix != "" {
			base += toolStatusSeparator + suffix
		}
		fullDetail := base + toolStatusSeparator + detail
		if displayWidth(fullDetail) <= width {
			return fullDetail
		}
		if available := width - displayWidth(base) - displayWidth(toolStatusSeparator); available > 0 {
			return base + toolStatusSeparator + TruncateOperationStatus(detail, available)
		}
	}
	if tail != "" {
		if available := width - displayWidth(prefix) - displayWidth(toolStatusSeparator); available > 0 {
			return prefix + toolStatusSeparator + TruncateOperationStatus(tail, available)
		}
	}
	return TruncateOperationStatus(full, width)
}

const minToolSummaryWidth = 12

func toolResultSummary(name string, data []byte) string {
	args := ToolOperationArgs(data)
	switch name {
	case "todo_write":
		if summary := TodoSummary(data); summary != "" {
			return summary
		}
	case "request_user_input":
		return OneLinePreview(ArgString(args, "question"))
	case "process_run":
		return ProcessCommandPreview(args)
	case "shell_run", "powershell_run":
		return ShellCommandPreview(ArgString(args, "command"))
	case "web_search":
		return OneLinePreview(ArgString(args, "query"))
	case "load_skill":
		skillName := OneLinePreview(ArgString(args, "name"))
		if file := OneLinePreview(ArgString(args, "file")); file != "" {
			if skillName != "" {
				return skillName + "  (" + file + ")"
			}
			return file
		}
		return skillName
	case "fs_apply_patch":
		return ""
	case "fs_read_file", "fs_write_file", "fs_replace", "fs_delete_file", "fs_delete_dir", "fs_mkdir", "fs_list_dir", "fs_stat":
		return OneLinePreview(ArgString(args, "path"))
	case "fs_search":
		query := OneLinePreview(ArgString(args, "query"))
		path := OneLinePreview(ArgString(args, "path"))
		switch {
		case query != "" && path != "":
			return query + " in " + path
		case query != "":
			return query
		case path != "":
			return "in " + path
		}
	case "fs_largest":
		path := OneLinePreview(ArgString(args, "path"))
		kind := OneLinePreview(ArgString(args, "kind"))
		if kind == "" {
			kind = "files"
		}
		if path != "" {
			return kind + " in " + path
		}
		return kind
	case "fs_copy", "fs_move":
		source := OneLinePreview(ArgString(args, "source_path"))
		dest := OneLinePreview(ArgString(args, "dest_path"))
		if source != "" && dest != "" {
			return source + " \u2192 " + dest
		}
	}
	return ToolArgsSummary(args)
}

func isShellTool(name string) bool {
	switch name {
	case "shell_run", "powershell_run":
		return true
	default:
		return false
	}
}

func displayWidth(s string) int { return ansi.StringWidth(s) }
