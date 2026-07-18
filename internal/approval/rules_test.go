package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellRulesAndMatching(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		wantType    RuleType
		wantPattern string
		allowed     string
		denied      string
	}{
		{"prefix", "git commit -m message", ShellPrefix, "git commit *", "git commit --amend", "git push"},
		{"redirection", "printf hi > output.txt", ShellExact, "printf hi >output.txt", "printf hi > output.txt", "printf hi > other.txt"},
		{"dynamic expansion", `rm -rf "$TARGET"`, ShellExact, `rm -rf "$TARGET"`, `rm -rf "$TARGET"`, `rm -rf "$OTHER"`},
		{"shell evaluator", `sh -c "rm -rf build"`, ShellExact, `sh -c "rm -rf build"`, `sh -c "rm -rf build"`, `sh -c "rm -rf dist"`},
		{"global option", "git -C repo commit -m message", ShellExact, "git -C repo commit -m message", "git -C repo commit -m message", "git -C other commit"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rules := ShellRulesWithMode(tc.command, true)
			require.Len(t, rules, 1)
			require.Equal(t, tc.wantType, rules[0].Type)
			require.Equal(t, tc.wantPattern, rules[0].Pattern)
			require.True(t, ShellAllowWithMode(tc.allowed, rules, true))
			require.False(t, ShellAllowWithMode(tc.denied, rules, true))
		})
	}

	compound := ShellRulesWithMode("git commit -m message && npm run build", true)
	require.Len(t, compound, 2)
	require.True(t, ShellAllowWithMode("git commit --amend && npm run build -- --watch", compound, true))
	require.False(t, ShellAllowWithMode("git commit --amend && rm -rf .", compound, true))

	t.Run("prefixes use command boundaries", func(t *testing.T) {
		for command, pattern := range map[string]string{
			"git commit":    "git commit *",
			"npm run build": "npm run build *",
			"rm -rf build":  "rm -rf *",
			"ls *":          "ls *",
		} {
			rules := ShellRulesWithMode(command, true)
			require.Len(t, rules, 1, command)
			require.Equal(t, pattern, rules[0].Pattern, command)
		}
		rules := ShellRulesWithMode("ls *", true)
		require.True(t, ShellAllowWithMode("ls ~/.*", rules, true))
		require.True(t, ShellAllowWithMode("ls", rules, true))
		require.False(t, ShellAllowWithMode("lsof", rules, true))
	})

	t.Run("quoted operators do not split", func(t *testing.T) {
		rules := ShellRulesWithMode(`printf '%s' 'a && b' && rm -rf build`, true)
		require.Len(t, rules, 2)
		require.True(t, ShellAllowWithMode(`printf '%s' 'different || text' && rm -rf dist`, rules, true))
	})

	t.Run("outer redirection and quoted whitespace stay exact", func(t *testing.T) {
		command := "{ printf hi; rm -rf build; } > output.txt"
		rules := ShellRulesWithMode(command, true)
		require.Len(t, rules, 1)
		require.Equal(t, ShellExact, rules[0].Type)
		require.False(t, ShellAllowWithMode("{ printf hi; rm -rf build; } > other.txt", rules, true))

		rules = ShellRulesWithMode(`printf "a  b" > output.txt`, true)
		require.True(t, ShellAllowWithMode(`printf "a  b" > output.txt`, rules, true))
		require.False(t, ShellAllowWithMode(`printf "a b" > output.txt`, rules, true))
	})

	t.Run("large compounds fall back to one exact rule", func(t *testing.T) {
		command := "a 1; b 2; c 3; d 4; e 5; f 6"
		rules := ShellRulesWithMode(command, true)
		require.Len(t, rules, 1)
		require.Equal(t, ShellExact, rules[0].Type)
		require.True(t, ShellAllowWithMode(command, rules, true))
		require.False(t, ShellAllowWithMode(command+"; g 7", rules, true))
	})

	t.Run("simple parser mode retains prefix and exact behavior", func(t *testing.T) {
		rules := ShellRulesWithMode("npm run build", false)
		require.True(t, ShellAllowWithMode("npm run build -- --watch", rules, false))
		require.False(t, ShellAllowWithMode("npm test", rules, false))

		rules = ShellRulesWithMode("rm -rf build", false)
		require.True(t, ShellAllowWithMode("rm -rf dist", rules, false))
		require.False(t, ShellAllowWithMode("rm build", rules, false))

		rules = ShellRulesWithMode("printf hi > output.txt", false)
		require.Len(t, rules, 1)
		require.Equal(t, ShellExact, rules[0].Type)
		require.Equal(t, "printf hi > output.txt", rules[0].Pattern)
	})
}

func TestPowerShellRulesAreToolScoped(t *testing.T) {
	rules := ShellRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users", false)
	require.Len(t, rules, 1)
	require.Equal(t, ShellPrefix, rules[0].Type)
	require.Equal(t, "Get-ChildItem *", rules[0].Pattern)
	require.True(t, ShellAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows", rules, false))
	require.False(t, ShellAllowForToolWithMode("shell_run", "Get-ChildItem C:\\Windows", rules, false))
	require.False(t, ShellAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows | Remove-Item -Recurse", rules, false))
	require.False(t, ShellAllowForToolWithMode("powershell_run", "Get-ChildItem C:\\Windows; Remove-Item old.txt", rules, false))

	compound := ShellRulesForToolWithMode("powershell_run", "Get-ChildItem C:\\Users | Where-Object Name", false)
	require.Len(t, compound, 1)
	require.Equal(t, ShellExact, compound[0].Type)
}

func TestRuleSetScopeAndDedupe(t *testing.T) {
	scope := WorkspaceScope("/workspace")
	scoped := func(rule Rule) Rule {
		rule.ScopeKind = scope.Kind
		rule.ScopeValue = scope.Value
		return rule
	}

	var rules RuleSet
	rules.Add(scoped(Rule{Type: EditAll, Tool: "file_edit"}))
	for _, tc := range []struct {
		tool string
		args string
	}{
		{"fs_write_file", `{"path":"a.txt"}`},
		{"fs_replace", `{"path":"a.txt","old_text":"a","new_text":"b"}`},
		{"fs_apply_patch", `{"patch":"..."}`},
		{"fs_delete_file", `{"path":"a.txt"}`},
		{"fs_delete_dir", `{"path":"a"}`},
		{"fs_move", `{"source_path":"a.txt","dest_path":"b.txt"}`},
		{"fs_copy", `{"source_path":"a.txt","dest_path":"b.txt"}`},
		{"fs_mkdir", `{"path":"a"}`},
	} {
		require.True(t, rules.Allows(tc.tool, []byte(tc.args), scope), tc.tool)
	}
	require.False(t, rules.Allows("shell_run", []byte(`{"command":"rm a.txt"}`), scope))
	require.False(t, rules.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), WorkspaceScope("/other")))

	rules.Add(scoped(Rule{Type: DirAllow, Paths: []string{"C:\\Users"}}))
	require.True(t, rules.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\old.txt"}`), scope))
	require.False(t, rules.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Windows\\old.txt"}`), scope))
	require.False(t, rules.Allows("powershell_run", []byte(`{"command":"Remove-Item C:\\Users\\old.txt"}`), WorkspaceScope("/other")))

	rules.Add(scoped(Rule{Type: ToolAll, Tool: "mcp_tool"}))
	require.True(t, rules.Allows("mcp_tool", []byte(`{"value":1}`), scope))
	require.False(t, rules.Allows("other_tool", nil, scope))

	var legacyRules RuleSet
	legacyRules.Add(Rule{Type: EditAll, Tool: "file_edit"})
	require.False(t, legacyRules.Allows("fs_write_file", []byte(`{"path":"a.txt"}`), scope))

	workspaceRule := scoped(Rule{Type: DirAllow, Paths: []string{"a", "b"}})
	otherWorkspaceRule := workspaceRule
	otherWorkspaceRule.ScopeValue = "/other"
	require.ElementsMatch(t, []Rule{workspaceRule, otherWorkspaceRule}, Dedupe([]Rule{
		workspaceRule,
		workspaceRule,
		otherWorkspaceRule,
	}))
}
