package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseProposals(t *testing.T) {
	t.Run("level-two headings still parse", func(t *testing.T) {
		content := "## Proposal 1: Brief Title\n- **Approach**: a\n- **Steps**: do a\n\n" +
			"## Proposal 2: Other Title\n- **Approach**: b\n- **Steps**: do b\n"
		got := parseProposals(content)
		require.Len(t, got, 2)
		require.Equal(t, "Proposal 1: Brief Title", got[0].title)
		require.Equal(t, "Proposal 2: Other Title", got[1].title)
		require.Contains(t, got[0].content, "do a")
		require.Contains(t, got[1].content, "do b")
	})

	t.Run("level-three headings are recognized", func(t *testing.T) {
		// Regression: models sometimes emit ### instead of ##, which the parser
		// previously missed, causing multi-proposal output to render as a single plan.
		content := "### Proposal 1: Foo\nbody one\n\n### Proposal 2: Bar\nbody two"
		got := parseProposals(content)
		require.Len(t, got, 2)
		require.Equal(t, "Proposal 1: Foo", got[0].title)
		require.Equal(t, "Proposal 2: Bar", got[1].title)
		require.Equal(t, "body one", got[0].content)
		require.Equal(t, "body two", got[1].content)
	})

	t.Run("level-four headings are recognized", func(t *testing.T) {
		content := "#### Proposal 1: X\nx\n\n#### Proposal 2: Y\ny"
		got := parseProposals(content)
		require.Len(t, got, 2)
		require.Equal(t, "Proposal 1: X", got[0].title)
		require.Equal(t, "Proposal 2: Y", got[1].title)
	})

	t.Run("unicode titles are preserved", func(t *testing.T) {
		content := "### Proposal 1: 进阶优化\nbody1\n\n### Proposal 2: 成本优化\nbody2"
		got := parseProposals(content)
		require.Len(t, got, 2)
		require.Equal(t, "Proposal 1: 进阶优化", got[0].title)
		require.Equal(t, "Proposal 2: 成本优化", got[1].title)
	})

	t.Run("single proposal returns nil", func(t *testing.T) {
		require.Nil(t, parseProposals("### Proposal 1: Lone\njust one"))
	})

	t.Run("no proposal headings returns nil", func(t *testing.T) {
		require.Nil(t, parseProposals("## Plan\n- **Approach**: only one approach\n"))
	})

	t.Run("inline mention does not count as a heading", func(t *testing.T) {
		content := "## Proposal 1: Real\nsee also ## Proposal 2 later\nmore body"
		got := parseProposals(content)
		// Only the line-starting heading counts; the inline mention is ignored.
		require.Nil(t, got)
	})
}

func TestLooksLikePlan(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "empty", content: "", want: false},
		{name: "whitespace only", content: "   \n\t ", want: false},
		{name: "narration only is not a plan", content: "好的，我先调查一下你当前的 opencode 配置和相关环境。", want: false},
		{name: "plain text without structure", content: "I will look into the config and suggest improvements.", want: false},
		{name: "inline plan word is not a heading", content: "here is my plan for you", want: false},
		{name: "single plan heading", content: "## Plan\n- **Approach**: do it\n- **Steps**: 1. x", want: true},
		{name: "h3 plan heading", content: "### Plan\nbody", want: true},
		{name: "proposal heading", content: "## Proposal 1: Foo\nbody", want: true},
		{name: "h3 proposal heading", content: "### Proposal 2: Bar\nbody", want: true},
		{name: "field markers without heading", content: "- **Approach**: a\n- **Steps**: b", want: true},
		{name: "commands field", content: "notes\n**Commands**: go test", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, looksLikePlan(tc.content))
		})
	}
}
