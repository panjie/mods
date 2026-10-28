package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/approval"

	"github.com/stretchr/testify/require"
)

func compactReviewText(s string) string {
	return strings.ReplaceAll(strings.Join(strings.Fields(ansi.Strip(s)), ""), "│", "")
}

func newPagedTestReviewer(command string) *toolReviewer {
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
		name: "shell_run", resp: make(chan reviewResponse, 1),
		candidateRules: []Rule{{Type: approval.DirAllow, Mode: AccessWrite, Paths: []string{"/project"}}},
		presentation: reviewPresentation{headline: "Modify local files", tone: interactionToneWarning, toneText: "Warning", rows: []interactionRow{
			{Label: "Target", Value: "/project"}, {Label: "Command", Value: command},
		}},
	}})
	return r
}

func TestReviewResponseZeroAndUnknownActionsDeny(t *testing.T) {
	for _, action := range []reviewAction{reviewActionDeny, reviewAction(255)} {
		r := &toolReviewer{
			reviewMode: ReviewAuto, scope: WorkingDirScope("/cwd"), reviewAvailabilityKnown: true,
			interactiveReviewAvailable: true, reviewChan: make(chan toolReviewItem, 1),
		}
		done := make(chan error, 1)
		go func() {
			done <- r.requestApproval(reviewerDeps{
				ctx:          context.Background(),
				accessIntent: AccessIntent{Class: AccessWrite, WriteCoverage: approval.CoverageIncomplete},
			}, "native_write", nil)
		}()
		item := receiveReviewItem(t, r.reviewChan)
		item.resp <- reviewResponse{action: action}
		require.ErrorContains(t, <-done, "execution denied by user")
	}
}

func TestReviewCancelStopsTaskInsteadOfDenying(t *testing.T) {
	for _, enter := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		r := newPagedTestReviewer("touch file")
		r.renderBanner(120, makeStyles(true).Interaction, 24)
		resp := r.reviewItem.resp
		m := &Mods{Config: &Config{}, ctx: ctx, ctxCancel: cancel, reviewer: r, userInput: newUserInputManager(&Config{})}
		key := tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}
		if enter {
			r.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
			require.Equal(t, reviewOptionCancel, r.reviewOptions()[r.selected].action)
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		_, cmd := m.Update(key)
		require.NotNil(t, cmd)
		r.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
		require.Empty(t, resp, "cancellation must not release the tool as a denial or approval")
		_, quit := m.Update(cmd())
		require.ErrorIs(t, ctx.Err(), context.Canceled)
		require.False(t, r.isPending())
		require.Equal(t, doneState, m.state)
		require.NotNil(t, quit)
		require.IsType(t, quitMsg{}, quit())
	}
}

func TestReviewDenyKeepsTaskRunning(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := newPagedTestReviewer("touch file")
	resp := r.reviewItem.resp
	m := &Mods{Config: &Config{}, ctx: ctx, ctxCancel: cancel, reviewer: r, userInput: newUserInputManager(&Config{})}
	m.Update(tea.KeyPressMsg{Code: 'n', Text: "n"})
	require.Equal(t, reviewActionDeny, (<-resp).action)
	require.NoError(t, ctx.Err())
}

func TestSessionGrantOnlyReusesIdenticalTargets(t *testing.T) {
	rules := []Rule{{Type: approval.DirAllow, Mode: AccessWrite, Paths: []string{"/tmp/project", "/usr/bin"}}}
	rows := []interactionRow{{Label: "Target", Value: "/tmp/project, /usr/bin"}}
	label := sessionRuleLabel(rules, rows)
	require.Equal(t, "Write above directories and subdirectories; this conversation only", label)
	rows[0].Value = "/tmp/project/reader.py"
	label = sessionRuleLabel(rules, rows)
	require.Contains(t, label, "/tmp/project, /usr/bin")
	require.Contains(t, label, "including subdirectories")
	require.NotContains(t, label, "above")
}

func TestCommandReviewCompactLayoutAndPaging(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		t.Run(name, func(t *testing.T) {
			args := []byte(`{"purpose":"生成报告供用户检查","command":"echo report > report.txt","program":"report","args":["--output","report.txt"],"effect":"may_write"}`)
			r := &toolReviewer{}
			r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
				name: name, resp: make(chan reviewResponse, 1),
				presentation: formatReviewPresentationWithIntent(name, args, WorkingDirScope("/project"), AccessIntent{}),
			}})
			styles := makeStyles(true).Interaction
			banner := ansi.Strip(r.renderBanner(100, styles, 24))
			require.Equal(t, 1, r.reviewPages)
			require.Contains(t, banner, "Run command?")
			require.Contains(t, banner, "Purpose: 生成报告供用户检查")
			require.Contains(t, banner, "Command:")
			require.Contains(t, banner, "/project", "the command heading keeps its execution directory")
			require.NotContains(t, banner, "Working dir:", "the default directory is folded into the command heading")
			require.Less(t, strings.Index(banner, "Purpose:"), strings.Index(banner, "Command:"))
			for _, hidden := range []string{"1/1", "Pages", "Warning", "Model declaration", "Interpreter", "Source:", "1 |"} {
				require.NotContains(t, banner, hidden)
			}
			for _, action := range []string{"Allow once", "Deny", "Cancel task"} {
				require.Contains(t, banner, action)
			}
			require.Contains(t, banner, "←/→ choose")
			require.Contains(t, banner, "❯")
			require.NotContains(t, banner, "run this once", "approval actions carry no redundant descriptions")
			require.NotContains(t, banner, "do not run it")
			require.NotContains(t, banner, "Allow for session")
			lines := strings.Split(banner, "\n")
			actionLine := lines[lineIndexContaining(lines, "Allow once")]
			require.Contains(t, actionLine, "Deny", "choice keys share one compact row")
			require.Contains(t, actionLine, "Cancel task")
			// All content remains reachable on a small terminal, with approval
			// available on every page and no change to the reviewed arguments.
			for i := range r.reviewItem.presentation.rows {
				if r.reviewItem.presentation.rows[i].Label == "Command" {
					r.reviewItem.presentation.rows[i].Value = strings.Repeat("echo full-command-line\n", 30) + "last-command-line"
				}
			}
			r.renderBanner(48, styles, 12)
			require.Greater(t, r.reviewPages, 1)
			var pages string
			for page := 0; page < r.reviewPages; page++ {
				r.reviewPage = page
				view := r.renderBanner(48, styles, 12)
				require.LessOrEqual(t, lipgloss.Height(view), 12)
				require.True(t, r.canApprovePages())
				require.Contains(t, ansi.Strip(view), fmt.Sprintf("%d/%d", page+1, r.reviewPages))
				pages += compactReviewText(view)
			}
			require.Contains(t, pages, "last-command-line")
		})
	}
}

func TestCommandReviewAlignsMultiDigitLineNumbers(t *testing.T) {
	commandLines := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		commandLines = append(commandLines, fmt.Sprintf("%d | echo %d", i, i))
	}
	r := &toolReviewer{}
	r.handleStartMsg(toolReviewStartMsg{item: toolReviewItem{
		name: "shell_run", resp: make(chan reviewResponse, 1),
		presentation: reviewPresentation{tone: interactionToneWarning, rows: []interactionRow{
			{Label: "Purpose", Value: "probe version"},
			{Label: "Working dir", Value: "/project"},
			{Label: "Command", Value: strings.Join(commandLines, "\n")},
		}},
	}})
	banner := ansi.Strip(r.renderBanner(80, makeStyles(true).Interaction, 40))
	var gutterColumns []int
	for _, line := range strings.Split(banner, "\n") {
		if strings.Count(line, "│") != 2 {
			continue
		}
		first := strings.Index(line, "│")
		rest := line[first+len("│"):]
		gutterColumns = append(gutterColumns, first+len("│")+strings.Index(rest, "│"))
	}
	require.Len(t, gutterColumns, 12, "each numbered command line renders one gutter")
	for _, column := range gutterColumns {
		require.Equal(t, gutterColumns[0], column, "line-number gutters must stay aligned past 9 lines")
	}
}
