package app

import (
	"context"
	"fmt"
	"strings"
	"time"

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
	m.sendToolOperationStatus(shellProgressStatus(progress.Tool, command, progress.Elapsed, lastOutput, m.width))
}

func shellProgressStatus(tool, command string, elapsed time.Duration, lastOutput string, width int) string {
	prefix := "Shell"
	if tool == "powershell_run" {
		prefix = "PS"
	}
	status := prefix + " - " + shellElapsed(elapsed)
	if output := shellProgressOutputPreview(lastOutput); output != "" {
		status += " - last: " + output
	}
	if command := ShellCommandPreview(command); command != "" {
		status += " - " + command
	}
	return TruncateOperationStatus(status, width)
}

func shellElapsed(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	seconds := int(elapsed.Round(time.Second).Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	seconds %= 60
	if minutes < 60 {
		return fmt.Sprintf("%dm%02ds", minutes, seconds)
	}
	hours := minutes / 60
	minutes %= 60
	return fmt.Sprintf("%dh%02dm", hours, minutes)
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
