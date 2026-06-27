package ui

import (
	"errors"
	"fmt"
	"strings"
)

// exitCoder is implemented by errors that carry a process exit code, such as
// tools.ShellExitError. It avoids coupling this package to the tools package.
type exitCoder interface {
	ExitCode() int
}

// ShellResultBlock returns a compact markdown transcript block describing the
// outcome of a completed shell command, so users can review which commands ran
// and whether they succeeded. It returns an empty string for tool names that
// are not shell commands.
func ShellResultBlock(name string, data []byte, err error) string {
	command := shellCommand(name, data)
	if command == "" {
		return ""
	}
	code := inlineCode(command)
	if err == nil {
		return fmt.Sprintf("> \u2713 ran %s \u00b7 exit 0", code)
	}
	var ec exitCoder
	if errors.As(err, &ec) {
		return fmt.Sprintf("> \u2717 ran %s \u00b7 exit %d", code, ec.ExitCode())
	}
	return fmt.Sprintf("> \u2717 ran %s \u00b7 failed: %s", code, OneLinePreview(err.Error()))
}

func shellCommand(name string, data []byte) string {
	switch name {
	case "shell_run", "powershell_run":
	default:
		return ""
	}
	return OneLinePreview(ArgString(ToolOperationArgs(data), "command"))
}

// inlineCode wraps s in a markdown inline-code span, using a backtick fence
// long enough to enclose any backtick runs within s.
func inlineCode(s string) string {
	maxRun := 0
	run := 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > maxRun {
				maxRun = run
			}
			continue
		}
		run = 0
	}
	fence := strings.Repeat("`", maxRun+1)
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") {
		pad = " "
	}
	return fence + pad + s + pad + fence
}
