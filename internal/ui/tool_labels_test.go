package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestShellCommandPreview(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "single line", command: "ls -la", want: "ls -la"},
		{name: "first line without comment", command: "echo a\n echo b", want: "echo a"},
		{name: "skips leading comment then shows real command", command: "# check go.mod\nls -la", want: "ls -la"},
		{name: "skips blank and comment lines", command: "\n# one\n  # two\n\ngo test ./...", want: "go test ./..."},
		{name: "collapses whitespace in the chosen line", command: "# intro\ngo   test   ./...", want: "go test ./..."},
		{name: "unicode comment is skipped", command: "# 检查 workspace 配置\nls .opencode*", want: "ls .opencode*"},
		{name: "shebang is treated as comment", command: "#!/bin/bash\necho hi", want: "echo hi"},
		{name: "all comment lines fall back to first line", command: "# only comment", want: "# only comment"},
		{name: "crlf line endings", command: "# c\r\necho hi\r\n", want: "echo hi"},
		{name: "empty string", command: "", want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, ShellCommandPreview(tc.command))
		})
	}
}

func TestTruncateOperationStatusUsesTerminalCellWidth(t *testing.T) {
	tests := []struct {
		name  string
		input string
		width int
		want  string
	}{
		{name: "ascii", input: "abcdefghijkl", width: 8, want: "abcde..."},
		{name: "wide characters", input: "皇族数の確保", width: 10, want: "皇族数..."},
		{name: "emoji graphemes", input: "🙂🙂🙂", width: 5, want: "🙂..."},
		{name: "narrow wide character", input: "皇族", width: 2, want: "皇"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := TruncateOperationStatus(tc.input, tc.width)
			require.Equal(t, tc.want, got)
			require.LessOrEqual(t, ansi.StringWidth(got), tc.width)
		})
	}
}
