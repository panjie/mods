package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

var testApprovalScope = WorkspaceScope("/workspace")

func scopedRule(rule ApprovalRule) ApprovalRule {
	rule.ScopeKind = testApprovalScope.Kind
	rule.ScopeValue = testApprovalScope.Value
	return rule
}

func scopedRules(rules []ApprovalRule) []ApprovalRule {
	result := make([]ApprovalRule, 0, len(rules))
	for _, rule := range rules {
		result = append(result, scopedRule(rule))
	}
	return result
}

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

	rules.Add(scopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"}))
	require.True(t, rules.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), testApprovalScope))
	require.True(t, rules.Allows("fs_apply_patch", []byte(`{"patch":"..."}`), testApprovalScope))
	require.False(t, rules.Allows("shell_run", []byte(`{"command":"rm a.txt"}`), testApprovalScope))
	require.False(t, rules.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), WorkspaceScope("/other")))

	rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
	require.True(t, rules.Allows("powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`), testApprovalScope))
	require.False(t, rules.Allows("powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`), testApprovalScope))
	require.False(t, rules.Allows("powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`), WorkspaceScope("/other")))
	require.False(t, rules.Allows("shell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`), testApprovalScope))

	rules.Add(scopedRule(ApprovalRule{Type: approvalToolAll, Tool: "mcp_tool"}))
	require.True(t, rules.Allows("mcp_tool", []byte(`{"value":1}`), testApprovalScope))
	require.False(t, rules.Allows("other_tool", nil, testApprovalScope))

	var legacyRules approvalRuleSet
	legacyRules.Add(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"})
	require.False(t, legacyRules.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), testApprovalScope))

	workspaceRule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"a", "b"}})
	otherWorkspaceRule := workspaceRule
	otherWorkspaceRule.ScopeValue = "/other"
	require.ElementsMatch(t, []ApprovalRule{workspaceRule, otherWorkspaceRule}, dedupeApprovalRules([]ApprovalRule{
		workspaceRule,
		workspaceRule,
		otherWorkspaceRule,
	}))
}

func TestReviewKeys(t *testing.T) {
	t.Run("yes does not remember", func(t *testing.T) {
		reviewer := &toolReviewer{
			reviewPending: true,
			scope:         testApprovalScope,
			reviewItem: &toolReviewItem{
				resp: make(chan reviewResponse, 1),
				candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
					Type:  approvalDirAllow,
					Paths: []string{"a"},
				})},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
		require.True(t, handled)
		require.Empty(t, reviewer.rules.Snapshot())
	})

	t.Run("always remembers displayed rules", func(t *testing.T) {
		rule := ApprovalRule{
			Type:  approvalDirAllow,
			Paths: []string{"a"},
		}
		rule = scopedRule(rule)
		reviewer := &toolReviewer{
			reviewPending: true,
			scope:         testApprovalScope,
			reviewItem: &toolReviewItem{
				resp:           make(chan reviewResponse, 1),
				candidateRules: []ApprovalRule{rule},
			},
		}
		handled, _ := reviewer.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		require.True(t, handled)
		require.Equal(t, []ApprovalRule{rule}, reviewer.rules.Snapshot())
	})
}

func TestReviewBannerShowsSavedRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"git commit -m message"}`),
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"git", "commit"},
			})},
		},
	}
	rendered := reviewer.renderBanner("", 120, lipgloss.NewStyle(), lipgloss.NewStyle())
	require.Contains(t, rendered, "[A] Always allow")
	require.Contains(t, rendered, "Always saves in /workspace: dirs: git, commit")
}

func TestReviewBannerStylesChoiceSeparators(t *testing.T) {
	renderer := lipgloss.NewRenderer(nil)
	renderer.SetColorProfile(termenv.TrueColor)
	reviewChoices := renderer.NewStyle().
		Foreground(lipgloss.Color("#E0DDFF")).
		Background(lipgloss.Color("#4A3B9F")).
		Padding(0, 2)
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"git commit -m message"}`),
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"git", "commit"},
			})},
		},
	}
	rendered := reviewer.renderBanner("", 120, renderer.NewStyle(), reviewChoices)

	baseStyle := reviewChoices.Copy().Padding(0, 0)
	selectedStyle := baseStyle.Copy().
		Foreground(lipgloss.Color("#4A3B9F")).
		Background(lipgloss.Color("#E0DDFF"))
	require.Contains(t, rendered, selectedStyle.Render("[Y] Approve")+baseStyle.Render("  ")+baseStyle.Render("[N] Deny"))
	require.NotContains(t, rendered, selectedStyle.Render("[Y] Approve")+"  "+baseStyle.Render("[N] Deny"))
}

func TestReviewBannerTruncatesSavedRule(t *testing.T) {
	reviewer := &toolReviewer{
		reviewPending: true,
		scope:         testApprovalScope,
		reviewItem: &toolReviewItem{
			name: "shell_run",
			args: []byte(`{"command":"cat >> ~/.config/ghostty/config.ghostty << 'EOF'\nfont-family = JetBrainsMono Nerd Font\nfont-family-bold = JetBrainsMono Nerd Font\nfont-family-italic = JetBrainsMono Nerd Font\nfont-family-bold-italic = JetBrainsMono Nerd Font\nEOF"}`),
			candidateRules: []ApprovalRule{scopedRule(ApprovalRule{
				Type:  approvalDirAllow,
				Paths: []string{"cat", ">>"},
			})},
		},
	}
	rendered := reviewer.renderBanner("", 80, lipgloss.NewStyle(), lipgloss.NewStyle())
	lines := strings.Split(rendered, "\n")
	for _, line := range lines {
		require.LessOrEqual(t, len([]rune(line)), 80, line)
	}
	require.Contains(t, rendered, "Always saves in /workspace: dirs: cat, >>")
}

func TestReviewPolicyNonTTY(t *testing.T) {
	oldIsInputTTY := isInputTTY
	isInputTTY = func() bool { return false }
	t.Cleanup(func() { isInputTTY = oldIsInputTTY })

	mods := &Mods{
		ctx:    context.Background(),
		Config: &Config{},
	}
	registry := testReviewRegistry(t)
	mods.currentToolRegistry = registry

	t.Run("review never allows mutable tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewNever, scope: testApprovalScope}
		require.False(t, reviewer.shouldReviewTool(registry, "fs_write_file"))
	})

	t.Run("mutable denies write without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "fs_write_file"))
		err := reviewer.requestApproval(mods, "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable allows read-only filesystem tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.False(t, reviewer.shouldReviewTool(registry, "fs_read_file"))
	})

	t.Run("mutable requires review for shell command when classifier unavailable", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "shell_run"))
		err := reviewer.requestApproval(mods, "shell_run", []byte(`{"command":"echo ok"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies compound shell after read-only prefix", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "shell_run"))
		err := reviewer.requestApproval(mods, "shell_run", []byte(`{"command":"echo ok; rm -rf ."}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable requires review for powershell command when classifier unavailable", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies nested powershell", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"powershell -EncodedCommand AAAA"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable routes powershell pipelines to review", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Users | Where-Object { $_.Name -like 'p*' }"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("mutable denies mutating powershell command without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewMutable, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Remove-Item C:\\tmp\\old.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching powershell command", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved powershell prefix does not allow pipeline mutation", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"C:\\Users"}}))
		require.True(t, reviewer.shouldReviewTool(registry, "powershell_run"))
		err := reviewer.requestApproval(mods, "powershell_run", []byte(`{"command":"Get-ChildItem C:\\Windows | Remove-Item -Recurse"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("always denies read-only tool without interactive approval", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		require.True(t, reviewer.shouldReviewTool(registry, "fs_read_file"))
		err := reviewer.requestApproval(mods, "fs_read_file", []byte(`{"path":"README.md"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("saved rule allows matching tool", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: testApprovalScope}
		reviewer.rules.Add(scopedRule(ApprovalRule{Type: approvalEditAll, Tool: "file_edit"}))
		require.True(t, reviewer.shouldReviewTool(registry, "fs_write_file"))
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
	_, cmd := mods.Update(streamEventMsg{
		kind:   streamEventToolCalls,
		runner: newStreamRunner(staticStream{}, nil, func(err error) tea.Msg { return modsError{Err: err} }),
		results: []proto.ToolCallStatus{{
			Name: "fs_write_file",
			Err:  fmt.Errorf("%w: fs_write_file requires approval", errReviewUnavailable),
		}},
	})

	msg := cmd()
	errMsg, ok := msg.(modsError)
	require.True(t, ok)
	require.Equal(t, "Tool execution requires review.", errMsg.ReasonText)
	require.True(t, errors.Is(errMsg.Err, errReviewUnavailable))
}

func testReviewRegistry(t *testing.T) *toolregistry.Registry {
	t.Helper()
	registry := toolregistry.NewRegistry()
	for _, tool := range []toolregistry.Tool{
		{Spec: proto.ToolSpec{Name: "fs_read_file"}, Capabilities: toolregistry.ToolCapabilities{ReadOnly: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "fs_write_file"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "shell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, Call: noopToolCall},
		{Spec: proto.ToolSpec{Name: "powershell_run"}, Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true}, Call: noopToolCall},
	} {
		require.NoError(t, registry.Register(tool))
	}
	return registry
}

func noopToolCall(context.Context, json.RawMessage) (string, error) { return "", nil }

func TestDirAllowPathExtraction(t *testing.T) {
	t.Run("extracts file paths from command", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"rm -rf /tmp/cache/foo"}`), testApprovalScope)
		require.Len(t, rules, 1)
		require.Equal(t, approvalDirAllow, rules[0].Type)
		require.ElementsMatch(t, []string{"/tmp/cache/foo"}, rules[0].Paths)
	})

	t.Run("extracts multiple paths", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"cp /tmp/a /var/b"}`), testApprovalScope)
		require.Len(t, rules, 1)
		require.Equal(t, approvalDirAllow, rules[0].Type)
		require.ElementsMatch(t, []string{"/tmp/a", "/var/b"}, rules[0].Paths)
	})

	t.Run("skips flags", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"rm -rf --preserve-root /tmp/x"}`), testApprovalScope)
		require.Len(t, rules, 1)
		require.ElementsMatch(t, []string{"/tmp/x"}, rules[0].Paths)
	})

	t.Run("skips subcommands", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"git commit -m message"}`), testApprovalScope)
		require.Len(t, rules, 1)
		require.Equal(t, approvalDirAllow, rules[0].Type)
		require.ElementsMatch(t, []string{"commit", "message"}, rules[0].Paths)
	})

	t.Run("dynamic expansion returns empty", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"rm -rf $TARGET"}`), testApprovalScope)
		require.Empty(t, rules)
	})

	t.Run("compound commands extract from all leaves", func(t *testing.T) {
		rules := RulesFor("shell_run", []byte(`{"command":"rm /tmp/a && rm /var/b"}`), testApprovalScope)
		require.Len(t, rules, 1)
		require.ElementsMatch(t, []string{"/tmp/a", "/var/b"}, rules[0].Paths)
	})
}

func TestDirAllowMatching(t *testing.T) {
	t.Run("allows when target is within allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm -rf /tmp/cache/foo"}`), testApprovalScope))
	})

	t.Run("allows when target equals allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), testApprovalScope))
	})

	t.Run("denies when target is outside allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm -rf /etc/passwd"}`), testApprovalScope))
	})

	t.Run("denies partial match", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/other"}`), testApprovalScope))
	})

	t.Run("allows when all targets are within any allowed dir", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/", "/var/cache/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("shell_run", []byte(`{"command":"cp /tmp/a /var/cache/b"}`), testApprovalScope))
	})

	t.Run("denies when any target is outside allowed dirs", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"cp /tmp/a /etc/b"}`), testApprovalScope))
	})

	t.Run("denies empty command", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":""}`), testApprovalScope))
	})

	t.Run("denies when no paths extracted", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"echo"}`), testApprovalScope))
	})

	t.Run("cross-scope denial", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"/tmp/"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.False(t, rs.Allows("shell_run", []byte(`{"command":"rm /tmp/foo"}`), WorkspaceScope("/other")))
	})

	t.Run("simple mode path extraction", func(t *testing.T) {
		rule := scopedRule(ApprovalRule{Type: approvalDirAllow, Paths: []string{"foo"}})
		var rs approvalRuleSet
		rs.Add(rule)
		require.True(t, rs.Allows("powershell_run", []byte(`{"command":"Get-ChildItem foo"}`), testApprovalScope))
		require.False(t, rs.Allows("powershell_run", []byte(`{"command":"Get-ChildItem bar"}`), testApprovalScope))
	})
}
