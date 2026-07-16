package cli

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestRenderListViewWidePlainOutput(t *testing.T) {
	view := listView{
		Title: "Things",
		Columns: []listColumn{
			{Header: "NAME"},
			{Header: "DESCRIPTION", Flexible: true},
		},
		Rows:    [][]string{{"alpha", "First thing."}, {"beta", "Second thing."}},
		Summary: "2 things",
	}

	got := renderListView(view, 80, false)

	require.Equal(t,
		"Things\n\nNAME   DESCRIPTION\nalpha  First thing.\nbeta   Second thing.\n\n2 things\n",
		got,
	)
	require.NotContains(t, got, "\x1b[")
}

func TestRenderListViewStyledPreservesPlainLayout(t *testing.T) {
	view := listView{
		Title:   "Things",
		Columns: []listColumn{{Header: "NAME"}},
		Rows:    [][]string{{"alpha"}},
		Summary: "1 thing",
	}

	plain := renderListView(view, 80, false)
	styled := renderListView(view, 80, true)

	require.Contains(t, styled, "\x1b[")
	require.Equal(t, plain, ansi.Strip(styled))
}

func TestRenderListViewNarrowUsesDescriptionContinuation(t *testing.T) {
	view := listView{
		Title: "技能",
		Columns: []listColumn{
			{Header: "NAME"},
			{Header: "SOURCE"},
			{Header: "DESCRIPTION", Flexible: true},
		},
		Rows: [][]string{{
			"非常长的技能名称",
			"built-in",
			"用于处理包含中文字符的很长描述，并且需要正确换行。",
		}},
		Summary: "1 skill",
	}

	got := renderListView(view, 32, false)

	require.Contains(t, got, "NAME              SOURCE")
	require.Contains(t, got, "非常长的技能名称  built-in")
	require.NotContains(t, got, "DESCRIPTION")
	require.Contains(t, got, "\n  用于处理")
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if strings.Contains(line, "非常长的技能名称") {
			continue // Fixed metadata is intentionally never truncated.
		}
		require.LessOrEqual(t, ansi.StringWidth(line), 32, line)
	}
}

func TestRenderListViewEmptyAndSingularSummary(t *testing.T) {
	view := listView{
		Title:   "Things",
		Columns: []listColumn{{Header: "NAME"}},
		Empty:   "No things found.",
		Summary: listCount(0, "thing", "things"),
	}
	require.Equal(t, "Things\n\nNo things found.\n\n0 things\n", renderListView(view, 80, false))
	require.Equal(t, "1 thing", listCount(1, "thing", "things"))
}
