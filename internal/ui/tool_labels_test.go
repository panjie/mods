package ui

import (
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestProcessCommandPreviewPreservesArgumentContents(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"a  b", `program "a  b"`},
		{" leading and trailing ", `program " leading and trailing "`},
		{"", `program ""`},
		{"line\nnext\tcolumn", `program "line\nnext\tcolumn"`},
		{"中文  文件", `program "中文  文件"`},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			require.Equal(t, tc.want, ProcessCommandPreview(map[string]any{"program": "program", "args": []any{tc.arg}}))
		})
	}
}

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
		{name: "unicode comment is skipped", command: "# 检查 cwd 配置\nls .opencode*", want: "ls .opencode*"},
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
		"web search":                  {"web_search", `{"query":"GUI wrapper for command line tools"}`, "search web  GUI wrapper for command line tools"},
		"shell preview":               {"shell_run", `{"command":"go   test   ./...\necho done"}`, "shell  go test ./..."},
		"shell leading comment":       {"shell_run", `{"command":"# check cwd config\nls .opencode*"}`, "shell  ls .opencode*"},
		"process argv":                {"process_run", `{"program":"go","args":["test","./path with space","$HOME"]}`, `process  go test "./path with space" $HOME`},
		"file read":                   {"fs_read_file", `{"path":"mods.go"}`, "read  mods.go"},
		"file write":                  {"fs_write_file", `{"path":"mods.go","content":"package main"}`, "write  mods.go"},
		"file replace":                {"fs_replace", `{"path":"mods.go","old_text":"old","new_text":"new"}`, "replace  mods.go"},
		"file search":                 {"fs_search", `{"path":"internal","query":"ToolOperationLabel"}`, "search  ToolOperationLabel in internal"},
		"largest file":                {"fs_largest", `{"path":"Downloads","kind":"file"}`, "largest  file in Downloads"},
		"copy":                        {"fs_copy", `{"source_path":"a.txt","dest_path":"b.txt"}`, "copy  a.txt → b.txt"},
		"move":                        {"fs_move", `{"source_path":"a.txt","dest_path":"b.txt"}`, "move  a.txt → b.txt"},
		"skill":                       {"load_skill", `{"name":"mcp-builder"}`, "skill  mcp-builder"},
		"skill file":                  {"load_skill", `{"name":"mcp-builder","file":"reference/foo.md"}`, "skill  mcp-builder  (reference/foo.md)"},
		"skill missing name":          {"load_skill", `{}`, "load skill"},
		"user input":                  {"request_user_input", `{"question":"你的16寸MacBook内存多大","kind":"select","options":["16GB","32GB"]}`, "ask  你的16寸MacBook内存多大"},
		"user input missing question": {"request_user_input", `{"kind":"text"}`, "run  request_user_input  (kind=text)"},
		"unknown preferred fields":    {"github_search", `{"query":"mods status bar","repo":"panjie/mods","irrelevant":"x"}`, "run  github_search  (query=mods status bar, repo=panjie/mods)"},
		"invalid json":                {"mcp_tool", `{nope`, "run  mcp_tool"},
		"empty args":                  {"mcp_tool", `{}`, "run  mcp_tool"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, ToolOperationLabel(tc.tool, []byte(tc.args), 120))
		})
	}

	got := ToolOperationLabel("web_search", []byte(`{"query":"搜索 一个 很长 很长 的 查询 内容"}`), 20)
	require.Equal(t, "search web  搜索 ...", got)
	require.LessOrEqual(t, ansi.StringWidth(got), 20)
}
