package main

import (
	"context"
	"errors"
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func TestShellApprovalRules(t *testing.T) {
	t.Run("command prefixes", func(t *testing.T) {
		tests := map[string]string{
			"git commit -m message": "git commit *",
			"git commit":            "git commit *",
			"npm run build":         "npm run build *",
			"rm -rf build":          "rm -rf *",
			"ls *":                  "ls *",
		}
		for command, pattern := range tests {
			rules := shellApprovalRulesWithMode(command, true)
			require.Len(t, rules, 1, command)
			require.Equal(t, approvalShellPrefix, rules[0].Type, command)
			require.Equal(t, pattern, rules[0].Pattern, command)
		}
	})

	t.Run("prefix uses word boundary", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("ls *", true)
		require.True(t, shellRulesAllowWithMode("ls ~/.*", rules, true))
		require.True(t, shellRulesAllowWithMode("ls", rules, true))
		require.False(t, shellRulesAllowWithMode("lsof", rules, true))
		require.False(t, shellRulesAllowWithMode("rm -rf .", rules, true))
	})

	t.Run("compound commands require every leaf", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("git commit -m message && npm run build", true)
		require.Len(t, rules, 2)
		require.True(t, shellRulesAllowWithMode("git commit --amend && npm run build -- --watch", rules, true))
		require.False(t, shellRulesAllowWithMode("git commit --amend && rm -rf .", rules, true))
	})

	t.Run("quoted operators do not split", func(t *testing.T) {
		rules := shellApprovalRulesWithMode(`printf '%s' 'a && b' && rm -rf build`, true)
		require.Len(t, rules, 2)
		require.True(t, shellRulesAllowWithMode(`printf '%s' 'different || text' && rm -rf dist`, rules, true))
	})

	t.Run("redirection falls back to exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("printf hi > output.txt", true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllowWithMode("printf hi > output.txt", rules, true))
		require.False(t, shellRulesAllowWithMode("printf hi > other.txt", rules, true))
	})

	t.Run("compound command with outer redirection is exact", func(t *testing.T) {
		command := "{ printf hi; rm -rf build; } > output.txt"
		rules := shellApprovalRulesWithMode(command, true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.False(t, shellRulesAllowWithMode("{ printf hi; rm -rf build; } > other.txt", rules, true))
	})

	t.Run("exact matching preserves quoted whitespace", func(t *testing.T) {
		rules := shellApprovalRulesWithMode(`printf "a  b" > output.txt`, true)
		require.True(t, shellRulesAllowWithMode(`printf "a  b" > output.txt`, rules, true))
		require.False(t, shellRulesAllowWithMode(`printf "a b" > output.txt`, rules, true))
	})

	t.Run("dynamic expansion falls back to exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode(`rm -rf "$TARGET"`, true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllowWithMode(`rm -rf "$TARGET"`, rules, true))
		require.False(t, shellRulesAllowWithMode(`rm -rf "$OTHER"`, rules, true))
	})

	t.Run("shell evaluators fall back to exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode(`sh -c "rm -rf build"`, true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.False(t, shellRulesAllowWithMode(`sh -c "rm -rf dist"`, rules, true))
	})

	t.Run("subcommand tools with global options are exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("git -C repo commit -m message", true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
	})

	t.Run("more than five commands uses full exact rule", func(t *testing.T) {
		command := "a 1; b 2; c 3; d 4; e 5; f 6"
		rules := shellApprovalRulesWithMode(command, true)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllowWithMode(command, rules, true))
		require.False(t, shellRulesAllowWithMode(command+"; g 7", rules, true))
	})
}

func TestShellApprovalRulesSimple(t *testing.T) {
	t.Run("command prefix", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("git commit -m message", false)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellPrefix, rules[0].Type)
		require.Equal(t, "git commit *", rules[0].Pattern)
	})

	t.Run("compound commands", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("git commit -m message && npm run build", false)
		require.Len(t, rules, 2)
		require.True(t, shellRulesAllowWithMode("git commit --amend && npm run build -- --watch", rules, false))
		require.False(t, shellRulesAllowWithMode("git commit --amend && rm -rf .", rules, false))
	})

	t.Run("redirection is exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("printf hi > output.txt", false)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.Equal(t, "printf hi > output.txt", rules[0].Pattern)
	})

	t.Run("sh evaluator is exact", func(t *testing.T) {
		rules := shellApprovalRulesWithMode(`sh -c "rm -rf build"`, false)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
	})

	t.Run("prefix match with different args", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("npm run build", false)
		require.True(t, shellRulesAllowWithMode("npm run build -- --watch", rules, false))
		require.False(t, shellRulesAllowWithMode("npm test", rules, false))
	})

	t.Run("prefix match rm -rf", func(t *testing.T) {
		rules := shellApprovalRulesWithMode("rm -rf build", false)
		require.True(t, shellRulesAllowWithMode("rm -rf dist", rules, false))
		require.False(t, shellRulesAllowWithMode("rm build", rules, false))
	})
}

func TestPowerShellApprovalRules(t *testing.T) {
	rules := shellApprovalRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users", false)
	require.Len(t, rules, 1)
	require.Equal(t, approvalShellPrefix, rules[0].Type)
	require.Equal(t, "powershell_run", rules[0].Tool)
	require.Equal(t, "Get-ChildItem *", rules[0].Pattern)
	require.True(t, shellRulesAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows", rules, false))
	require.False(t, shellRulesAllowForToolWithMode("shell_run", "Get-ChildItem C:\\Windows", rules, false))
	require.False(t, shellRulesAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows | Remove-Item -Recurse", rules, false))
	require.False(t, shellRulesAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows; Remove-Item old.txt", rules, false))

	compoundRules := shellApprovalRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users | Where-Object Name", false)
	require.Len(t, compoundRules, 1)
	require.Equal(t, approvalShellExact, compoundRules[0].Type)
}

func TestApprovalRuleSet(t *testing.T) {
	var rules approvalRuleSet

	rules.add(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})
	require.True(t, rules.allows("fs_write_file", []byte(`{"path":"a.txt"}`)))
	require.True(t, rules.allows("fs_apply_patch", []byte(`{"patch":"..."}`)))
	require.False(t, rules.allows("shell_run", []byte(`{"command":"rm a.txt"}`)))

	rules.add(shellApprovalRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users", false)...)
	require.True(t, rules.allows("powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`)))
	require.False(t, rules.allows("shell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`)))

	rules.add(ApprovalRule{Type: approvalToolAll, Tool: "mcp_tool"})
	require.True(t, rules.allows("mcp_tool", []byte(`{"value":1}`)))
	require.False(t, rules.allows("other_tool", nil))
}

func TestReviewKeys(t *testing.T) {
	t.Run("yes does not remember", func(t *testing.T) {
		reviewer := &toolReviewer{
			reviewPending: true,
			reviewItem: &toolReviewItem{
				resp: make(chan reviewResponse, 1),
				alwaysRules: []ApprovalRule{{
					Type: approvalShellPrefix,
					Tool: "shell_run", Pattern: "git commit *",
				}},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		require.True(t, handled)
		require.Empty(t, reviewer.rules.snapshot())
	})

	t.Run("always remembers displayed rules", func(t *testing.T) {
		rule := ApprovalRule{
			Type: approvalShellPrefix,
			Tool: "shell_run", Pattern: "git commit *",
		}
		reviewer := &toolReviewer{
			reviewPending: true,
			reviewItem: &toolReviewItem{
				resp:        make(chan reviewResponse, 1),
				alwaysRules: []ApprovalRule{rule},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		require.True(t, handled)
		require.Equal(t, []ApprovalRule{rule}, reviewer.rules.snapshot())
	})
}

func TestReviewBannerShowsSavedRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"git commit -m message"}`),
			alwaysRules: []ApprovalRule{{
				Type: approvalShellPrefix,
				Tool: "shell_run", Pattern: "git commit *",
			}},
		},
	}
	rendered := reviewer.renderBanner("", 120, lipgloss.NewStyle(), lipgloss.NewStyle())
	require.Contains(t, rendered, "[A] Always allow: shell_run(git commit *)")
}

func TestReviewPolicyNonTTY(t *testing.T) {
	oldIsInputTTY := isInputTTY
	isInputTTY = func() bool { return false }
	t.Cleanup(func() { isInputTTY = oldIsInputTTY })

	mods := &Mods{
		ctx:    context.Background(),
		Config: &Config{},
	}

	t.Run("review never allows mutable tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewNever}
		require.False(t, reviewer.shouldReviewTool("fs_write_file"))
	})

	t.Run("mutable denies write without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("fs_write_file"))
		err := reviewer.requestApproval(mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable allows read-only filesystem tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.False(t, reviewer.shouldReviewTool("fs_read_file"))
	})

	t.Run("mutable requires review for shell command when classifier unavailable", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("shell_run"))
		err := reviewer.requestApproval(mods, "shell_run", []byte(`{"command":"echo ok"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies compound shell after read-only prefix", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("shell_run"))
		err := reviewer.requestApproval(mods, "shell_run", []byte(`{"command":"echo ok; rm -rf ."}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable requires review for powershell command when classifier unavailable", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies nested powershell", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"powershell -EncodedCommand AAAA"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable routes powershell pipelines to review", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users | Where-Object { $_.Name -like 'p*' }"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies mutating powershell command without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable}
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Remove-Item C:\\tmp\\old.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching powershell command", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways}
		reviewer.rules.add(shellApprovalRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users", false)...)
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`))
		require.NoError(t, err)
	})

	t.Run("saved powershell prefix does not allow pipeline mutation", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways}
		reviewer.rules.add(shellApprovalRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users", false)...)
		require.True(t, reviewer.shouldReviewTool("powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows | Remove-Item -Recurse"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("always denies read-only tool without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways}
		require.True(t, reviewer.shouldReviewTool("fs_read_file"))
		err := reviewer.requestApproval(mods, "fs_read_file", []byte(`{"path":"README.md"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways}
		reviewer.rules.add(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})
		require.True(t, reviewer.shouldReviewTool("fs_write_file"))
		err := reviewer.requestApproval(mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.NoError(t, err)
	})
}

func TestReviewUnavailableIsFatal(t *testing.T) {
	mods := &Mods{
		Config:   &Config{},
		reviewer: &toolReviewer{},
		ctx:      context.Background(),
	}
	_, cmd := mods.Update(toolCallsOutput{
		results: []proto.ToolCallStatus{{
			Name: "fs_write_file",
			Err:  fmt.Errorf("%w: fs_write_file requires approval", errReviewUnavailable),
		}},
	})

	msg := cmd()
	errMsg, ok := msg.(modsError)
	require.True(t, ok)
	require.Equal(t, "Tool execution requires review.", errMsg.reason)
	require.True(t, errors.Is(errMsg.err, errReviewUnavailable))
}
