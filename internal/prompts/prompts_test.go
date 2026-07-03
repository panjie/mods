package prompts

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuiltinPrompts(t *testing.T) {
	defs := Builtin()
	byName := make(map[string]Definition, len(defs))
	for _, def := range defs {
		require.NotEmpty(t, def.Name)
		require.NotEmpty(t, def.Default)
		byName[def.Name] = def
	}

	for _, name := range []string{
		KeyIdentity,
		KeyToolSelection,
		KeyPlan,
		KeyShellClassifier,
		KeyMinimal,
		KeyFormatMarkdown,
		KeyFormatJSON,
		KeySafeWorkspaceTemplate,
		KeyApprovedPlanTemplate,
	} {
		require.Contains(t, byName, name)
	}

	require.True(t, byName[KeyIdentity].Configurable)
	require.True(t, byName[KeyShellClassifier].Configurable)
	require.False(t, byName[KeyMinimal].Configurable)
	require.Equal(t, Plan, byName[KeyPlan].Default)
	require.Equal(t, ShellClassifier, byName[KeyShellClassifier].Default)

	require.Contains(t, Identity, "execute it directly rather than asking for permission")
	require.Contains(t, Identity, "built-in review step that gates mutating changes")
	require.Contains(t, Identity, "state it briefly in one line, then proceed")
	require.Contains(t, ToolSelection, "attempt it directly rather than asking for permission first")
	require.Contains(t, ToolSelection, "rg --files")
	require.Contains(t, ToolSelection, "powershell_run")
	require.Contains(t, ToolSelection, "go test ./...")
	require.Contains(t, ToolSelection, "Do not keep retrying blindly")
}

func TestIdentityCoversAllFlags(t *testing.T) {
	for _, name := range []string{
		"api",
		"model",
		"ask-model",
		"http-proxy",
		"chat",
		"continue",
		"continue-last",
		"title",
		"list",
		"show",
		"show-last",
		"delete",
		"delete-older-than",
		"no-cache",
		"cache-path",
		"format",
		"format-as",
		"minimal",
		"raw",
		"quiet",
		"hide-tool-status",
		"hide-tool-results",
		"word-wrap",
		"status-text",
		"workspace",
		"editor",
		"image",
		"stdin-image",
		"clipboard-image",
		"settings",
		"config",
		"dirs",
		"reset-settings",
		"theme",
		"help",
		"help-all",
		"version",
		"list-prompts",
		"role",
		"list-roles",
		"web-search",
		"web-search-provider",
		"web-search-api-key",
		"web-search-api-key-env",
		"plan",
		"reasoning",
		"review",
		"max-tool-rounds",
		"mcp-list",
		"mcp-list-tools",
		"mcp-enable",
		"mcp-disable",
		"max-retries",
		"max-tokens",
		"max-input-chars",
		"no-limit",
		"temp",
		"topp",
		"topk",
		"stop",
		"debug",
	} {
		require.Contains(t, Identity, "--"+name,
			"identity.md must document --%s", name)
	}
}

func TestIdentityCoversConfigKeys(t *testing.T) {
	for _, key := range []string{
		"default-api",
		"default-model",
		"format-text",
		"roles",
		"prompts",
		"builtin-tools",
		"mcp-servers",
		"mcp-timeout",
		"apis",
		"review-mode",
		"shell-classify-prompt",
		"~/.config/mods/mods.yml",
	} {
		require.Contains(t, Identity, key,
			"identity.md must document config key %q", key)
	}
}

func TestIdentityHasSelfHelpPolicy(t *testing.T) {
	require.Contains(t, Identity, "fs_read_file ~/.config/mods/mods.yml",
		"must instruct LLM how to read config")
	require.Contains(t, Identity, `Get-Content (Join-Path $env:USERPROFILE ".config\mods\mods.yml")`,
		"must give Windows config examples in PowerShell syntax")
	require.Contains(t, Identity, "Self-help policy",
		"must have a self-help section")
}
