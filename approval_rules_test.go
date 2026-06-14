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
			rules := shellApprovalRules(command)
			require.Len(t, rules, 1, command)
			require.Equal(t, approvalShellPrefix, rules[0].Type, command)
			require.Equal(t, pattern, rules[0].Pattern, command)
		}
	})

	t.Run("prefix uses word boundary", func(t *testing.T) {
		rules := shellApprovalRules("ls *")
		require.True(t, shellRulesAllow("ls ~/.*", rules))
		require.True(t, shellRulesAllow("ls", rules))
		require.False(t, shellRulesAllow("lsof", rules))
		require.False(t, shellRulesAllow("rm -rf .", rules))
	})

	t.Run("compound commands require every leaf", func(t *testing.T) {
		rules := shellApprovalRules("git commit -m message && npm run build")
		require.Len(t, rules, 2)
		require.True(t, shellRulesAllow("git commit --amend && npm run build -- --watch", rules))
		require.False(t, shellRulesAllow("git commit --amend && rm -rf .", rules))
	})

	t.Run("quoted operators do not split", func(t *testing.T) {
		rules := shellApprovalRules(`printf '%s' 'a && b' && rm -rf build`)
		require.Len(t, rules, 2)
		require.True(t, shellRulesAllow(`printf '%s' 'different || text' && rm -rf dist`, rules))
	})

	t.Run("redirection falls back to exact", func(t *testing.T) {
		rules := shellApprovalRules("printf hi > output.txt")
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllow("printf hi > output.txt", rules))
		require.False(t, shellRulesAllow("printf hi > other.txt", rules))
	})

	t.Run("compound command with outer redirection is exact", func(t *testing.T) {
		command := "{ printf hi; rm -rf build; } > output.txt"
		rules := shellApprovalRules(command)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.False(t, shellRulesAllow("{ printf hi; rm -rf build; } > other.txt", rules))
	})

	t.Run("exact matching preserves quoted whitespace", func(t *testing.T) {
		rules := shellApprovalRules(`printf "a  b" > output.txt`)
		require.True(t, shellRulesAllow(`printf "a  b" > output.txt`, rules))
		require.False(t, shellRulesAllow(`printf "a b" > output.txt`, rules))
	})

	t.Run("dynamic expansion falls back to exact", func(t *testing.T) {
		rules := shellApprovalRules(`rm -rf "$TARGET"`)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllow(`rm -rf "$TARGET"`, rules))
		require.False(t, shellRulesAllow(`rm -rf "$OTHER"`, rules))
	})

	t.Run("shell evaluators fall back to exact", func(t *testing.T) {
		rules := shellApprovalRules(`sh -c "rm -rf build"`)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.False(t, shellRulesAllow(`sh -c "rm -rf dist"`, rules))
	})

	t.Run("subcommand tools with global options are exact", func(t *testing.T) {
		rules := shellApprovalRules("git -C repo commit -m message")
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
	})

	t.Run("more than five commands uses full exact rule", func(t *testing.T) {
		command := "a 1; b 2; c 3; d 4; e 5; f 6"
		rules := shellApprovalRules(command)
		require.Len(t, rules, 1)
		require.Equal(t, approvalShellExact, rules[0].Type)
		require.True(t, shellRulesAllow(command, rules))
		require.False(t, shellRulesAllow(command+"; g 7", rules))
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
