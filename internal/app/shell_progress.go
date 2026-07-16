package app

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func (m *Mods) handleShellProgress(_ context.Context, progress toolregistry.ShellProgress) {
	command := progress.Command
	lastOutput := progress.LastOutput
	if m.secrets != nil {
		command = m.secrets.Redact(command)
		lastOutput = m.secrets.Redact(lastOutput)
	}
	m.sendToolOperationStatus(shellProgressStatus(progress.Tool, command, lastOutput, m.width))
}

func shellProgressStatus(tool, command string, lastOutput string, width int) string {
	prefix := shellStatusPrefix(tool)
	if prefix == "" {
		prefix = "Shell"
	}
	status := prefix
	if command := ShellCommandPreview(command); command != "" {
		status += " - " + command
	}
	if output := shellProgressOutputPreview(lastOutput); output != "" {
		status += " - last: " + output
	}
	return TruncateOperationStatus(status, width)
}

func shellCompletionStatus(tool string, data []byte, err error, width int) string {
	prefix := shellStatusPrefix(tool)
	if prefix == "" {
		return ""
	}
	command := ShellCommandPreview(ArgString(ToolOperationArgs(data), "command"))
	if command == "" {
		return ""
	}
	if err == nil {
		return TruncateOperationStatus("✓ "+prefix+" - "+command, width)
	}
	var exitErr shellExitCoder
	if errors.As(err, &exitErr) {
		return TruncateOperationStatus(fmt.Sprintf("✗ %s - %s (exit %d)", prefix, command, exitErr.ExitCode()), width)
	}
	return TruncateOperationStatus(fmt.Sprintf("✗ %s - %s (failed: %s)", prefix, command, OneLinePreview(err.Error())), width)
}

func shellStatusPrefix(tool string) string {
	switch tool {
	case "shell_run":
		return "Shell"
	case "powershell_run":
		return "PS"
	default:
		return ""
	}
}

func shellProgressOutputPreview(output string) string {
	output = ansi.Strip(output)
	output = strings.Map(func(r rune) rune {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' {
			return -1
		}
		return r
	}, output)
	output = strings.ReplaceAll(output, "\r", "\n")
	lines := strings.Split(output, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return OneLinePreview(lines[i])
		}
	}
	return ""
}
