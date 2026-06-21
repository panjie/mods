package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
)

func TestIsCompletionCmd(t *testing.T) {
	for args, is := range map[string]bool{
		"":                                     false,
		"something":                            false,
		"something something":                  false,
		"completion for my bash script how to": false,
		"completion bash how to":               false,
		"completion":                           false,
		"completion -h":                        true,
		"completion --help":                    true,
		"completion help":                      true,
		"completion bash":                      true,
		"completion fish":                      true,
		"completion zsh":                       true,
		"completion powershell":                true,
		"completion bash -h":                   true,
		"completion fish -h":                   true,
		"completion zsh -h":                    true,
		"completion powershell -h":             true,
		"completion bash --help":               true,
		"completion fish --help":               true,
		"completion zsh --help":                true,
		"completion powershell --help":         true,
		"__complete":                           true,
		"__complete blah blah blah":            true,
	} {
		t.Run(args, func(t *testing.T) {
			vargs := append([]string{"mods"}, strings.Fields(args)...)
			if b := isCompletionCmd(vargs); b != is {
				t.Errorf("%v: expected %v, got %v", vargs, is, b)
			}
		})
	}
}

func TestConversationCompletions(t *testing.T) {
	saveDB := db
	defer func() { db = saveDB }()

	db = testDB(t)
	const id = "df31ae23ab8b75b5643c2f846c570997edc71333"
	require.NoError(t, db.Save(id, "message 1", "openai", "gpt-4o"))

	results := conversationCompletions("df31")
	require.Equal(t, []string{"df31ae2\tmessage 1"}, results)
}

func TestIsVersionOrHelpCmd(t *testing.T) {
	for args, is := range map[string]bool{
		"":                        false,
		"mods":                    false,
		"mods something":          false,
		"mods --version":          true,
		"mods -v":                 true,
		"mods --help":             true,
		"mods -h":                 true,
		"mods --help-all":         true,
		"mods --model gpt-4":      false,
		"mods -v -m gpt-4":        true,
		"mods -m gpt-4 --version": true,
	} {
		t.Run(args, func(t *testing.T) {
			vargs := append([]string{"mods"}, strings.Fields(args)...)
			if b := isVersionOrHelpCmd(vargs); b != is {
				t.Errorf("%v: expected %v, got %v", vargs, is, b)
			}
		})
	}
}

func TestThemeFrom(t *testing.T) {
	t.Run("charm", func(t *testing.T) {
		require.NotNil(t, themeFrom("charm"))
	})
	t.Run("dracula", func(t *testing.T) {
		require.NotNil(t, themeFrom("dracula"))
	})
	t.Run("catppuccin", func(t *testing.T) {
		require.NotNil(t, themeFrom("catppuccin"))
	})
	t.Run("base16", func(t *testing.T) {
		require.NotNil(t, themeFrom("base16"))
	})
	t.Run("unknown defaults to charm", func(t *testing.T) {
		require.NotNil(t, themeFrom("nonexistent"))
	})
}

func TestMinimalFlagRegistered(t *testing.T) {
	if rootCmd.Flags().Lookup("minimal") == nil {
		initFlags()
	}
	require.NotNil(t, rootCmd.Flags().Lookup("minimal"))
}

func TestFancinessFlagRemoved(t *testing.T) {
	if rootCmd.Flags().Lookup("minimal") == nil {
		initFlags()
	}
	require.Nil(t, rootCmd.Flags().Lookup("fanciness"))
}

func TestRoleNames(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()

	config = Config{
		PersistentConfig: PersistentConfig{
			Roles: map[string][]string{
				"default": {"You are helpful."},
				"shell":   {"You are a shell expert."},
				"go-dev":  {"You write Go code."},
			},
		},
	}

	t.Run("all roles", func(t *testing.T) {
		roles := roleNames("")
		require.Len(t, roles, 3)
		require.Contains(t, roles, "default")
		require.Contains(t, roles, "shell")
		require.Contains(t, roles, "go-dev")
	})

	t.Run("prefix filter", func(t *testing.T) {
		roles := roleNames("go")
		require.Len(t, roles, 1)
		require.Equal(t, "go-dev", roles[0])
	})

	t.Run("no match prefix", func(t *testing.T) {
		roles := roleNames("nonexistent")
		require.Empty(t, roles)
	})
}

func TestIsNoArgs(t *testing.T) {
	t.Run("empty config", func(t *testing.T) {
		cfg := Config{}
		require.True(t, isNoArgsCfg(cfg))
	})
	t.Run("with prefix", func(t *testing.T) {
		cfg := Config{Prefix: "hello"}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with show", func(t *testing.T) {
		cfg := Config{Show: "abc123"}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with delete", func(t *testing.T) {
		cfg := Config{Delete: []string{"abc"}}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with help", func(t *testing.T) {
		cfg := Config{ShowHelp: true}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with help all", func(t *testing.T) {
		cfg := Config{HelpAll: true}
		require.False(t, isNoArgsCfg(cfg))
	})
}

func isNoArgsCfg(cfg Config) bool {
	return cfg.Prefix == "" &&
		cfg.Show == "" &&
		!cfg.ShowLast &&
		len(cfg.Delete) == 0 &&
		cfg.DeleteOlderThan == 0 &&
		!cfg.ShowHelp &&
		!cfg.HelpAll &&
		!cfg.List &&
		!cfg.ListRoles &&
		!cfg.MCPList &&
		!cfg.MCPListTools &&
		!cfg.Dirs &&
		!cfg.Settings &&
		!cfg.ResetSettings
}

func TestHelpUsageFiltersAdvancedFlags(t *testing.T) {
	ensureTestFlags()

	flags := rootCmd.Flags()
	require.True(t, flagVisibleInUsage(flags.Lookup("model"), false))
	require.True(t, flagVisibleInUsage(flags.Lookup("help-all"), false))
	require.True(t, flagVisibleInUsage(flags.Lookup("workspace"), false))

	for _, name := range []string{"temp", "max-tool-rounds", "web-search-provider"} {
		flag := flags.Lookup(name)
		require.NotNil(t, flag)
		require.False(t, flagVisibleInUsage(flag, false), name)
		require.True(t, flagVisibleInUsage(flag, true), name)
	}

	require.False(t, flagVisibleInUsage(flags.Lookup("memprofile"), true))
}

func TestUsageIntroAndPromptSyntax(t *testing.T) {
	ensureTestFlags()
	saveConfig := config
	defer func() { config = saveConfig }()
	config.HelpAll = false

	output := captureStdout(t, func() {
		require.NoError(t, usageFunc(rootCmd))
	})

	require.Contains(t, output, helpIntroSummary)
	require.Contains(t, output, "inspect and edit files")
	require.Contains(t, output, "run shell commands")
	require.Contains(t, output, "[PROMPT...]")
	require.NotContains(t, output, "[PREFIX TERM]")
	require.Equal(t, helpIntroSummary, rootCmd.Short)
}

func TestHelpAllGroupsFlagsByCategory(t *testing.T) {
	ensureTestFlags()

	groups := groupedUsageFlags(rootCmd.Flags(), true)
	require.True(t, groupHasFlag(groups, flagCategorySession, "continue"))
	require.True(t, groupHasFlag(groups, flagCategoryMCP, "mcp-list"))
	require.True(t, groupHasFlag(groups, flagCategoryModelParams, "temp"))

	for category, flags := range groups {
		require.False(t, groupHasFlag(map[string][]*pflag.Flag{category: flags}, category, "memprofile"))
	}
}

func TestAdvancedFlagsStillParse(t *testing.T) {
	saveConfig := config
	defer func() { config = saveConfig }()
	ensureTestFlags()

	require.NoError(t, rootCmd.Flags().Set("temp", "0.2"))
	require.NoError(t, rootCmd.Flags().Set("max-tool-rounds", "12"))
	require.NoError(t, rootCmd.Flags().Set("web-search-provider", "tavily"))

	require.Equal(t, 0.2, config.Temperature)
	require.Equal(t, 12, config.MaxToolRounds)
	require.Equal(t, "tavily", config.WebSearchProvider)
}

func TestReadmeDoesNotListRemovedPromptFlags(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	readme := string(content)
	require.Contains(t, readme, "[PROMPT...]")
	require.Contains(t, readme, "grouped by purpose")
	require.Contains(t, readme, "edit your files")
	require.Contains(t, readme, "run shell")
	require.Contains(t, readme, "review step")
	require.NotContains(t, readme, "[PREFIX TERM]")
	require.NotContains(t, readme, "`--prompt`")
	require.NotContains(t, readme, "`--prompt-args`")
	require.NotContains(t, readme, "`-P`, `--prompt`")
	require.NotContains(t, readme, "`-p`, `--prompt-args`")
}

func ensureTestFlags() {
	if rootCmd.Flags().Lookup("minimal") == nil {
		initFlags()
	}
}

func groupHasFlag(groups map[string][]*pflag.Flag, category, name string) bool {
	for _, f := range groups[category] {
		if f.Name == name {
			return true
		}
	}
	return false
}

func captureStdout(tb testing.TB, fn func()) string {
	tb.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	require.NoError(tb, err)
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	require.NoError(tb, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(tb, err)
	require.NoError(tb, r.Close())
	return string(out)
}
