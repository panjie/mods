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

func TestRemoveWhitespace(t *testing.T) {
	require.Empty(t, RemoveWhitespace(" \n"))
	require.Equal(t, " regular\n ", RemoveWhitespace(" regular\n "))
}

func TestIncreaseIndent(t *testing.T) {
	require.Equal(t, "\thello", IncreaseIndent("hello"))
	require.Equal(t, "\ta\n\tb\n\tc", IncreaseIndent("a\nb\nc"))
	require.Equal(t, "\t", IncreaseIndent(""))
}

func TestToolOperationLabel(t *testing.T) {
	tests := map[string]struct {
		tool string
		args string
		want string
	}{
		"web search":               {"web_search", `{"query":"GUI wrapper for command line tools"}`, "Searching web: GUI wrapper for command line tools"},
		"shell preview":            {"shell_run", `{"command":"go   test   ./...\necho done"}`, "Shell: go test ./..."},
		"shell leading comment":    {"shell_run", `{"command":"# check workspace config\nls .opencode*"}`, "Shell: ls .opencode*"},
		"process argv":             {"process_run", `{"program":"go","args":["test","./path with space","$HOME"]}`, `Process: go test "./path with space" $HOME`},
		"file read":                {"fs_read_file", `{"path":"mods.go"}`, "Reading file: mods.go"},
		"file write":               {"fs_write_file", `{"path":"mods.go","content":"package main"}`, "Writing file: mods.go"},
		"file replace":             {"fs_replace", `{"path":"mods.go","old_text":"old","new_text":"new"}`, "Replacing text in: mods.go"},
		"file search":              {"fs_search", `{"path":"internal","query":"ToolOperationLabel"}`, "Searching files: ToolOperationLabel in internal"},
		"largest file":             {"fs_largest", `{"path":"Downloads","kind":"file"}`, "Finding largest file in: Downloads"},
		"copy":                     {"fs_copy", `{"source_path":"a.txt","dest_path":"b.txt"}`, "Copying: a.txt to b.txt"},
		"move":                     {"fs_move", `{"source_path":"a.txt","dest_path":"b.txt"}`, "Moving: a.txt to b.txt"},
		"skill":                    {"load_skill", `{"name":"mcp-builder"}`, "Loading skill: mcp-builder"},
		"skill file":               {"load_skill", `{"name":"mcp-builder","file":"reference/foo.md"}`, "Loading skill: mcp-builder (reference/foo.md)"},
		"skill missing name":       {"load_skill", `{}`, "Loading skill"},
		"unknown preferred fields": {"github_search", `{"query":"mods status bar","repo":"panjie/mods","irrelevant":"x"}`, "Running tool: github_search (query=mods status bar, repo=panjie/mods)"},
		"invalid json":             {"mcp_tool", `{nope`, "Running tool: mcp_tool"},
		"empty args":               {"mcp_tool", `{}`, "Running tool: mcp_tool"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, ToolOperationLabel(tc.tool, []byte(tc.args), 120))
		})
	}

	got := ToolOperationLabel("web_search", []byte(`{"query":"搜索 一个 很长 很长 的 查询 内容"}`), 20)
	require.Equal(t, "Searching web: 搜...", got)
	require.LessOrEqual(t, ansi.StringWidth(got), 20)
}
