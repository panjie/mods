package app

import (
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		{name: "missing required fields", content: "## Plan\n- **Approach**: do it\n- **Steps**: 1. x", want: false},
		{name: "h3 plan missing fields", content: "### Plan\nbody", want: false},
		{name: "proposal missing fields", content: "## Proposal 1: Foo\nbody", want: false},
		{name: "field markers without heading", content: "- **Approach**: a\n- **Steps**: b\n- **Risks**: c\n- **Files**: d", want: true},
		{name: "single complete plan", content: "## Plan\n- **Approach**: do it\n- **Steps**: 1. x\n- **Files**: a.go\n- **Risks**: low", want: true},
		{name: "complete plan with commands instead of files", content: "## Plan\n- **Approach**: do it\n- **Steps**: 1. x\n- **Commands**: go test\n- **Risks**: low", want: true},
		{name: "complete proposals", content: "## Proposal 1: Foo\n- **Approach**: a\n- **Steps**: b\n- **Files**: c\n- **Risks**: d\n\n## Proposal 2: Bar\n- **Approach**: e\n- **Steps**: f\n- **Commands**: g\n- **Risks**: h", want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, looksLikePlan(tc.content))
		})
	}
}

func TestPlanPromptDiscouragesOverInvestigation(t *testing.T) {
	// Pin the over-investigation guidance so it is not accidentally removed.
	require.Contains(t, planSystemPrompt, "directly relevant")
	require.Contains(t, planSystemPrompt, "hardware")
	require.Contains(t, planSystemPrompt, "3 to 5")
}

// proposalModeMods constructs the minimum Mods state required for the
// proposal-mode key handler to operate.
func proposalModeMods(t *testing.T, proposals ...proposal) *Mods {
	t.Helper()
	m := &Mods{
		Config:         &Config{},
		Styles:         makeStyles(lipgloss.NewRenderer(nil)),
		reviewer:       &toolReviewer{},
		contentMutex:   &sync.Mutex{},
		operationMutex: sync.Mutex{},
		proposals:      proposals,
		proposalMode:   true,
	}
	return m
}

// TestProposalEnterSelectsCurrent asserts the enter key in proposal mode
// behaves the same as Y: it selects the proposal currently displayed,
// regardless of the residual m.planSelected value. The previous switch on
// m.planSelected was unreachable dead code.
func TestProposalEnterSelectsCurrent(t *testing.T) {
	proposals := []proposal{
		{title: "Proposal 1", content: "approach A"},
		{title: "Proposal 2", content: "approach B"},
	}

	for _, residual := range []int{0, 1, 2, 3, 4, 99} {
		t.Run(name("planSelected=", residual), func(t *testing.T) {
			m := proposalModeMods(t, proposals...)
			m.proposalSelected = 1
			m.planSelected = residual

			cmd, handled := m.handleProposalKey(tea.KeyMsg{Type: tea.KeyEnter})
			require.True(t, handled)
			require.Nil(t, cmd)
			require.False(t, m.proposalMode, "selecting must exit proposal mode")
			require.Equal(t, "approach B", m.planContent,
				"enter must commit the currently displayed proposal (Proposal 2)")
		})
	}
}

// TestProposalEnterEquivalentToY pins the design that enter and Y produce
// the same state transition so future refactors keep the two in sync.
func TestProposalEnterEquivalentToY(t *testing.T) {
	proposals := []proposal{
		{title: "Proposal 1", content: "alpha"},
		{title: "Proposal 2", content: "beta"},
	}
	mEnter := proposalModeMods(t, proposals...)
	mEnter.proposalSelected = 1
	mY := proposalModeMods(t, proposals...)
	mY.proposalSelected = 1

	_, _ = mEnter.handleProposalKey(tea.KeyMsg{Type: tea.KeyEnter})
	_, _ = mY.handleProposalKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})

	require.Equal(t, mY.planContent, mEnter.planContent)
	require.Equal(t, mY.proposalMode, mEnter.proposalMode)
}

// TestProposalSelectionBarNoMisleadingHighlight verifies the rendered nav
// bar does not pull from m.planSelected (which has no key to advance it in
// proposal mode) to highlight a single option. Concretely: changing
// planSelected must not change the rendered output.
func TestProposalSelectionBarNoMisleadingHighlight(t *testing.T) {
	proposals := []proposal{
		{title: "Proposal 1", content: "x"},
		{title: "Proposal 2", content: "y"},
	}
	mA := proposalModeMods(t, proposals...)
	mA.width = 100
	mA.planSelected = 0

	mB := proposalModeMods(t, proposals...)
	mB.width = 100
	mB.planSelected = 3

	require.Equal(t, mA.renderProposalSelectionBar(""), mB.renderProposalSelectionBar(""),
		"the proposal bar must be invariant under m.planSelected because no key advances it in proposal mode")
}

func TestPlanInteractionPanelsFitTerminalWidths(t *testing.T) {
	for _, width := range []int{30, 60, 80, 120} {
		t.Run(name("width=", width), func(t *testing.T) {
			m := proposalModeMods(t,
				proposal{title: "Proposal 1", content: "x"},
				proposal{title: "Proposal 2", content: "y"},
			)
			m.width = width
			m.proposalSelected = 0
			outputs := []string{
				m.renderProposalSelectionBar(""),
				m.renderPlanReviewBanner(""),
			}
			ti := textinput.New()
			ti.Placeholder = "Describe changes"
			m.feedbackInput = ti
			outputs = append(outputs, m.renderPlanFeedbackInput(""))
			for _, output := range outputs {
				for _, line := range strings.Split(output, "\n") {
					require.LessOrEqual(t, lipgloss.Width(line), width, line)
				}
			}
			require.Contains(t, outputs[0], "PLAN READY")
			require.Contains(t, outputs[1], "Approve")
			require.Contains(t, outputs[2], "MODIFICATION FEEDBACK")
		})
	}
}

func name(prefix string, n int) string {
	return prefix + strconv.Itoa(n)
}
