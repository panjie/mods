package ui

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// exitCoder is implemented by errors that carry a process exit code, such as
// tools.ShellExitError. It avoids coupling this package to the tools package.
type exitCoder interface {
	ExitCode() int
}

type correctionSuggester interface {
	CorrectionSuggested() bool
}

// ToolResultLine returns a compact single-line markdown transcript block for a
// completed tool call. It includes the tool name, a short argument summary, and
// whether the call succeeded or failed.
func ToolResultLine(name string, data []byte, err error) string {
	return ToolResultLineWidth(name, data, err, 120)
}

// ToolResultLineWidth is ToolResultLine with a caller-provided display width
// for the status text. The width should already account for any renderer prefix
// such as a Markdown blockquote gutter.
func ToolResultLineWidth(name string, data []byte, err error, width int) string {
	status := ToolResultStatus(name, data, err, width)
	if status == "" {
		return ""
	}
	return "> " + status
}

// ToolResultStatus returns the unstyled one-line status text used by the live
// operation footer. It starts with a success/failure marker so callers can
// apply styling uniformly.
func ToolResultStatus(name string, data []byte, err error, width int) string {
	if name == "" {
		return ""
	}
	prefix := "\u2713 " + name
	if err != nil {
		prefix = "\u2717 " + name
	}
	var correction correctionSuggester
	if errors.As(err, &correction) && correction.CorrectionSuggested() {
		prefix = "\u21bb " + name
	}
	summary := toolResultSummary(name, data)
	if summary != "" {
		prefix += ": "
	}
	suffix := ""
	detail := ""
	if err == nil {
		if isShellTool(name) {
			suffix = " \u00b7 exit 0"
		}
		return toolStatusLine(prefix, summary, suffix, detail, width)
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		suffix = fmt.Sprintf(" \u00b7 exit %d", ec.ExitCode())
		return toolStatusLine(prefix, summary, suffix, detail, width)
	}
	if correction != nil && correction.CorrectionSuggested() {
		suffix = " \u00b7 retry suggested"
		detail = OneLinePreview(err.Error())
		return toolStatusLine(prefix, summary, suffix, detail, width)
	}
	suffix = " \u00b7 failed"
	detail = OneLinePreview(err.Error())
	return toolStatusLine(prefix, summary, suffix, detail, width)
}

func toolStatusLine(prefix, summary, suffix, detail string, width int) string {
	if detail != "" {
		return toolStatusLineWithDetail(prefix, summary, suffix, detail, width)
	}
	if summary == "" {
		return TruncateOperationStatus(strings.TrimSuffix(prefix, ": ")+suffix, width)
	}
	available := width - displayWidth(prefix) - displayWidth(suffix)
	if available <= 0 {
		return TruncateOperationStatus(strings.TrimSuffix(prefix, ": ")+suffix, width)
	}
	return prefix + TruncateOperationStatus(summary, available) + suffix
}

// toolStatusLineWithDetail renders a line that carries an error detail. The
// failure reason outranks the argument summary: the summary shrinks first,
// is dropped next, and only then is the detail itself truncated so the
// reason behind a failure is never hidden by a long argument preview.
func toolStatusLineWithDetail(prefix, summary, suffix, detail string, width int) string {
	fullSuffix := suffix + ": " + detail
	if available := width - displayWidth(prefix) - displayWidth(fullSuffix); summary != "" && available >= minToolSummaryWidth {
		return prefix + TruncateOperationStatus(summary, available) + fullSuffix
	}
	base := strings.TrimSuffix(prefix, ": ")
	if displayWidth(base)+displayWidth(fullSuffix) <= width {
		return base + fullSuffix
	}
	detailWidth := width - displayWidth(base) - displayWidth(suffix) - displayWidth(": ")
	if detailWidth < 1 {
		return TruncateOperationStatus(base+fullSuffix, width)
	}
	return base + suffix + ": " + TruncateOperationStatus(detail, detailWidth)
}

const minToolSummaryWidth = 12

func toolResultSummary(name string, data []byte) string {
	if name == "todo_write" {
		if summary := TodoSummary(data); summary != "" {
			return summary
		}
	}
	args := ToolOperationArgs(data)
	if name == "request_user_input" {
		if question := OneLinePreview(ArgString(args, "question")); question != "" {
			return question
		}
	}
	if name == "process_run" {
		return ProcessCommandPreview(args)
	}
	if isShellTool(name) {
		return ShellCommandPreview(ArgString(args, "command"))
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
