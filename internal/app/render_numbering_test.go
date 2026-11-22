package app

import (
	"strings"
	"testing"

	"charm.land/glamour/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestEscapeOrderedTaskCheckboxes(t *testing.T) {
	t.Run("escapes checkbox markers on ordered items", func(t *testing.T) {
		got := escapeOrderedTaskCheckboxes("1. [ ] fix\n2. [x] done\n10. [X] also done\n3) [ ] parenthesized marker")
		require.Equal(t, "1. \\[ ] fix\n2. \\[x] done\n10. \\[X] also done\n3) \\[ ] parenthesized marker", got)
	})

	t.Run("allows up to three spaces of indent", func(t *testing.T) {
		got := escapeOrderedTaskCheckboxes("  4. [ ] nested item")
		require.Equal(t, "  4. \\[ ] nested item", got)
	})

	t.Run("leaves bullet task lists untouched", func(t *testing.T) {
		in := "- [ ] bullet task\n* [x] star task"
		require.Equal(t, in, escapeOrderedTaskCheckboxes(in))
	})

	t.Run("leaves non-checkbox brackets untouched", func(t *testing.T) {
		in := "1. [✓] not a GFM checkbox\n2. [~] status marker\n3. [see note] link-ish text\nsee [ ] mid-line"
		require.Equal(t, in, escapeOrderedTaskCheckboxes(in))
	})

	t.Run("leaves indented code blocks untouched", func(t *testing.T) {
		in := "    1. [ ] indented code line"
		require.Equal(t, in, escapeOrderedTaskCheckboxes(in))
	})

	t.Run("leaves fenced code blocks untouched", func(t *testing.T) {
		in := "```go\n1. [ ] code sample\n```\n2. [ ] after fence"
		got := escapeOrderedTaskCheckboxes(in)
		require.Equal(t, "```go\n1. [ ] code sample\n```\n2. \\[ ] after fence", got)
	})

	t.Run("no brackets is returned unchanged", func(t *testing.T) {
		require.Equal(t, "plain text", escapeOrderedTaskCheckboxes("plain text"))
	})
}

// TestOrderedTaskListKeepsNumbering guards against glamour dropping ordered
// list numbers for GFM task items: an ordered todo list whose pending steps
// use "[ ]" markers must keep every item number through the render pipeline.
func TestOrderedTaskListKeepsNumbering(t *testing.T) {
	gr, err := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(80),
	)
	require.NoError(t, err)
	m := &Mods{glam: gr}

	md := "任务计划：\n\n" +
		"1. [✓] 分析配置加载逻辑\n" +
		"2. [✓] 定位路径处理代码\n" +
		"3. [~] 检查渲染函数\n" +
		"4. [ ] 复现编号缺失问题\n" +
		"5. [x] 编写回归测试\n" +
		"6. [ ] 修复渲染逻辑\n"

	out, err := m.RenderMarkdown(preprocessMarkdownForRender(md))
	require.NoError(t, err)
	plain := ansi.Strip(out)

	for _, want := range []string{
		"1. [✓] 分析配置加载逻辑",
		"2. [✓] 定位路径处理代码",
		"3. [~] 检查渲染函数",
		"4. [ ] 复现编号缺失问题",
		"5. [x] 编写回归测试",
		"6. [ ] 修复渲染逻辑",
	} {
		require.Contains(t, plain, want, "rendered plan must keep the item number")
	}

	// No rendered line may carry checkbox content without a number.
	for _, line := range strings.Split(plain, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[ ]") || strings.HasPrefix(trimmed, "[✓]") {
			t.Errorf("unnumbered task item rendered: %q", trimmed)
		}
	}
}

// TestPreprocessMarkdownForRenderPreservesParagraphs pins the newline
// handling that shares the pipeline with the checkbox escape: single
// newlines become hard breaks, paragraph boundaries stay clean.
func TestPreprocessMarkdownForRenderPreservesParagraphs(t *testing.T) {
	got := preprocessMarkdownForRender("one\ntwo\n\nthree")
	require.Equal(t, "one  \ntwo\n\nthree", got)
}
