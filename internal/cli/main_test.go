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
		"__completeNoDesc":                     true,
		"__completeNoDesc blah blah blah":      true,
	} {
		t.Run(args, func(t *testing.T) {
			vargs := append([]string{"mods"}, strings.Fields(args)...)
			if b := isCompletionCmd(vargs); b != is {
				t.Errorf("%v: expected %v, got %v", vargs, is, b)
			}
		})
	}
}

func TestSessionCompletions(t *testing.T) {
	saveDB := db
	defer func() { db = saveDB }()

	db = testDB(t)
	const id = "df31ae23ab8b75b5643c2f846c570997edc71333"
	require.NoError(t, db.Save(id, "message 1", "openai", "gpt-4o"))

	results := sessionCompletions("df31")
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
	require.NotNil(t, rootCmd.Flags().Lookup("minimal"))
}

func TestReasoningShortFlagUsesLowercaseR(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		rawFlag := rootCmd.Flags().Lookup("raw")
		require.NotNil(t, rawFlag)
		require.Empty(t, rawFlag.Shorthand)

		reasoningFlag := rootCmd.Flags().Lookup("reasoning")
		require.NotNil(t, reasoningFlag)
		require.Equal(t, "r", reasoningFlag.Shorthand)

		require.NoError(t, rootCmd.Flags().Parse(normalizeOptionalReasoningValueArgs([]string{"-r", "on"})))
		require.Equal(t, ReasoningOn, config.Reasoning)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--raw"}))
		require.True(t, config.Raw)
	})

	withTestConfig(t, Config{}, func() {
		require.Error(t, rootCmd.Flags().Parse([]string{"-T"}))
	})
}

func TestClipboardImageShortFlag(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		flag := rootCmd.Flags().Lookup("clipboard-image")
		require.NotNil(t, flag)
		require.Equal(t, "I", flag.Shorthand)

		require.NoError(t, rootCmd.Flags().Parse([]string{"-I"}))
		require.True(t, config.ClipboardImage)
	})
}

func TestChatFlagRegistered(t *testing.T) {
	flag := rootCmd.Flags().Lookup("chat")
	require.NotNil(t, flag)
	require.Empty(t, flag.Shorthand)
}

func TestImageShortFlagStillUsesLowercaseI(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"-i", "assets/mods-product.png"}))
		require.Equal(t, []string{"assets/mods-product.png"}, config.Images)
		require.False(t, config.ClipboardImage)
	})
}

func TestNormalizeOptionalReasoningValueArgs(t *testing.T) {
	tests := map[string]struct {
		in   []string
		want []string
	}{
		"long flag consumes removed auto value so flag parser reports it": {
			in:   []string{"--reasoning", "auto", "hello"},
			want: []string{"--reasoning=auto", "hello"},
		},
		"short flag consumes valid spaced value": {
			in:   []string{"-r", "off", "hello"},
			want: []string{"-r=off", "hello"},
		},
		"bare long flag keeps prompt text": {
			in:   []string{"--reasoning", "hello"},
			want: []string{"--reasoning", "hello"},
		},
		"bare short flag keeps prompt text": {
			in:   []string{"-r", "hello"},
			want: []string{"-r", "hello"},
		},
		"old short flag is unchanged": {
			in:   []string{"-T", "off", "hello"},
			want: []string{"-T", "off", "hello"},
		},
		"removed auto equals form is unchanged": {
			in:   []string{"--reasoning=auto", "hello"},
			want: []string{"--reasoning=auto", "hello"},
		},
		"end of options stops normalization": {
			in:   []string{"--", "--reasoning", "auto"},
			want: []string{"--", "--reasoning", "auto"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tc.want, normalizeOptionalReasoningValueArgs(tc.in))
		})
	}
}

func TestThemeFlagValidatesChoices(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		for _, theme := range []string{"charm", "catppuccin", "dracula", "base16"} {
			t.Run(theme, func(t *testing.T) {
				require.NoError(t, rootCmd.Flags().Set("theme", theme))
				require.Equal(t, theme, config.Theme)
			})
		}

		t.Run("invalid", func(t *testing.T) {
			require.Error(t, rootCmd.Flags().Set("theme", "solarized"))
		})
	})
}

func TestFancinessFlagRemoved(t *testing.T) {
	require.Nil(t, rootCmd.Flags().Lookup("fanciness"))
}

func TestToolResultsFlagRenamed(t *testing.T) {
	require.Nil(t, rootCmd.Flags().Lookup("hide-tool-results"))
	require.NotNil(t, rootCmd.Flags().Lookup("show-tool-results"))
	require.Error(t, rootCmd.Flags().Parse([]string{"--hide-tool-results"}))
}

func TestNoSaveFlag(t *testing.T) {
	flag := rootCmd.Flags().Lookup("no-save")
	require.NotNil(t, flag)
	require.Equal(t, "n", flag.Shorthand)
	require.Nil(t, rootCmd.Flags().Lookup("no-session-save"))

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--no-save"}))
		require.True(t, config.NoSave)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"-n"}))
		require.True(t, config.NoSave)
	})

	withTestConfig(t, Config{}, func() {
		require.Error(t, rootCmd.Flags().Parse([]string{"--no-session-save"}))
	})
}

func TestRoleNames(t *testing.T) {
	withTestConfig(t, Config{
		PersistentConfig: PersistentConfig{
			Roles: map[string][]string{
				"default": {"You are helpful."},
				"shell":   {"You are a shell expert."},
				"go-dev":  {"You write Go code."},
			},
		},
	}, func() {
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
	t.Run("with list prompts", func(t *testing.T) {
		cfg := Config{ListPrompts: true}
		require.False(t, isNoArgsCfg(cfg))
	})
}

func isNoArgsCfg(cfg Config) bool {
	return cfg.Prefix == "" &&
		!cfg.ShowHelp &&
		!cfg.HelpAll &&
		!cfg.List &&
		!cfg.ListRoles &&
		!cfg.ListPrompts &&
		!cfg.MCPList &&
		!cfg.MCPListTools &&
		!cfg.Dirs &&
		!cfg.Settings &&
		!cfg.ConfigSetup &&
		!cfg.Chat &&
		!cfg.ResetSettings
}

func TestHelpUsageFiltersAdvancedFlags(t *testing.T) {
	flags := rootCmd.Flags()
	require.True(t, flagVisibleInUsage(flags.Lookup("model"), false))
	require.True(t, flagVisibleInUsage(flags.Lookup("help-all"), false))
	require.True(t, flagVisibleInUsage(flags.Lookup("workspace"), false))

	for _, name := range []string{"word-wrap", "max-tool-rounds", "web-search-provider"} {
		flag := flags.Lookup(name)
		require.NotNil(t, flag)
		require.False(t, flagVisibleInUsage(flag, false), name)
		require.True(t, flagVisibleInUsage(flag, true), name)
	}

	require.False(t, flagVisibleInUsage(flags.Lookup("memprofile"), true))
}

func TestUsageIntroAndPromptSyntax(t *testing.T) {
	withTestConfig(t, Config{HelpAll: false}, func() {
		output := captureStdout(t, func() {
			require.NoError(t, usageFunc(rootCmd))
		})

		require.Contains(t, output, helpIntroSummary)
		require.Contains(t, output, "inspect and edit files")
		require.Contains(t, output, "run shell commands")
		require.Contains(t, output, "[PROMPT...]")
		require.NotContains(t, output, "[PREFIX TERM]")
		require.Equal(t, helpIntroSummary, rootCmd.Short)
	})
}

func TestHelpAllGroupsFlagsByCategory(t *testing.T) {
	groups := groupedUsageFlags(rootCmd.Flags(), true)
	require.True(t, groupHasFlag(groups, flagCategorySession, "continue"))
	require.True(t, groupHasFlag(groups, flagCategorySession, "no-save"))
	require.True(t, groupHasFlag(groups, flagCategoryMCP, "list-mcps"))
	require.True(t, groupHasFlag(groups, flagCategoryModelParams, "max-tokens"))
	require.True(t, groupHasFlag(groups, flagCategoryInputOutput, "show-tool-results"))
	require.False(t, groupHasFlag(groups, flagCategoryInputOutput, "hide-tool-results"))

	for category, flags := range groups {
		require.False(t, groupHasFlag(map[string][]*pflag.Flag{category: flags}, category, "memprofile"))
	}
}

func TestDirsActionUsesSessions(t *testing.T) {
	withTestConfig(t, Config{
		SettingsPath: filepath.Join(t.TempDir(), "mods.yml"),
		SessionDir:   filepath.Join(t.TempDir(), "sessions"),
	}, func() {
		output := captureStdout(t, func() {
			require.NoError(t, runDirsAction([]string{"sessions"}))
		})
		require.Equal(t, config.SessionDir+"\n", output)

		output = captureStdout(t, func() {
			require.NoError(t, runDirsAction(nil))
		})
		require.Contains(t, output, "Sessions: "+config.SessionDir)
		require.NotContains(t, output, "Cache:")

		require.Error(t, runDirsAction([]string{"cache"}))
	})
}

func TestAdvancedFlagsStillParse(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Set("max-tool-rounds", "12"))
		require.NoError(t, rootCmd.Flags().Set("web-search-provider", "tavily"))

		require.Equal(t, 12, config.MaxToolRounds)
		require.Equal(t, "tavily", config.WebSearchProvider)
	})
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
	require.NotContains(t, readme, "workspace-root")
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
	readDone := make(chan struct {
		out []byte
		err error
	}, 1)
	go func() {
		out, err := io.ReadAll(r)
		readDone <- struct {
			out []byte
			err error
		}{out: out, err: err}
	}()

	fn()

	require.NoError(tb, w.Close())
	result := <-readDone
	require.NoError(tb, result.err)
	require.NoError(tb, r.Close())
	return string(result.out)
}

func TestListPromptsOutputsBuiltinMarkdown(t *testing.T) {
	output := captureStdout(t, listPrompts)

	require.Contains(t, output, "## identity\n\n")
	require.Contains(t, output, "## plan\n\n")
	require.Contains(t, output, "## shell-classifier\n\n")
	require.Contains(t, output, "## safe-workspace-template\n\n")
	require.Contains(t, output, "You are running inside mods")
	require.Contains(t, output, "Analyze this shell command for review.")
}

// TestNextBackupPathAvoidsOverwrite locks in the fix for resetSettings:
// the previous .bak (which may contain plaintext API keys) is never
// silently clobbered. Successive resets land at .bak, .bak.1, .bak.2, ...
func TestSessionPathFlagsRemoved(t *testing.T) {
	require.Nil(t, rootCmd.Flags().Lookup("cache-path"))
	require.Nil(t, rootCmd.Flags().Lookup("session-dir"))
	require.Nil(t, rootCmd.Flags().Lookup("session-path"))
	require.Error(t, rootCmd.Flags().Parse([]string{"--cache-path", "/tmp/mods-cache"}))
	require.Error(t, rootCmd.Flags().Parse([]string{"--session-dir", "/tmp/mods-sessions"}))
}

func TestNextBackupPathAvoidsOverwrite(t *testing.T) {
	t.Run("returns base when none exists", func(t *testing.T) {
		base := filepath.Join(t.TempDir(), "mods.yml.bak")
		got, err := nextBackupPath(base)
		require.NoError(t, err)
		require.Equal(t, base, got)
	})

	t.Run("appends numeric suffix when base exists", func(t *testing.T) {
		dir := t.TempDir()
		base := filepath.Join(dir, "mods.yml.bak")
		require.NoError(t, os.WriteFile(base, []byte("first"), 0o600))

		second, err := nextBackupPath(base)
		require.NoError(t, err)
		require.Equal(t, base+".1", second)

		// Simulate the previous slot also being taken.
		require.NoError(t, os.WriteFile(second, []byte("second"), 0o600))
		third, err := nextBackupPath(base)
		require.NoError(t, err)
		require.Equal(t, base+".2", third)
	})
}
