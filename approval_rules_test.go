package main

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

func TestApprovalRuleSet(t *testing.T) {
	var rules approvalRuleSet

	rules.add(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})
	require.True(t, rules.allows("fs_write_file", []byte(`{"path":"a.txt"}`)))
	require.True(t, rules.allows("fs_apply_patch", []byte(`{"patch":"..."}`)))
	require.False(t, rules.allows("shell_run", []byte(`{"command":"rm a.txt"}`)))

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
