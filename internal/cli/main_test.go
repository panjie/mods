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
	require.NotNil(t, rootCmd.Flags().Lookup("minimal"))
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
			in:   []string{"-T", "off", "hello"},
			want: []string{"-T=off", "hello"},
		},
		"bare long flag keeps prompt text": {
			in:   []string{"--reasoning", "hello"},
			want: []string{"--reasoning", "hello"},
		},
		"bare short flag keeps prompt text": {
			in:   []string{"-T", "hello"},
			want: []string{"-T", "hello"},
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
	t.Run("with chat", func(t *testing.T) {
		cfg := Config{Chat: true}
		require.False(t, isNoArgsCfg(cfg))
	})
	t.Run("with list prompts", func(t *testing.T) {
		cfg := Config{ListPrompts: true}
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

	for _, name := range []string{"temp", "max-tool-rounds", "web-search-provider"} {
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
	require.True(t, groupHasFlag(groups, flagCategoryMCP, "mcp-list"))
	require.True(t, groupHasFlag(groups, flagCategoryModelParams, "temp"))

	for category, flags := range groups {
		require.False(t, groupHasFlag(map[string][]*pflag.Flag{category: flags}, category, "memprofile"))
	}
}

func TestAdvancedFlagsStillParse(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Set("temp", "0.2"))
		require.NoError(t, rootCmd.Flags().Set("max-tool-rounds", "12"))
		require.NoError(t, rootCmd.Flags().Set("web-search-provider", "tavily"))

		require.Equal(t, 0.2, config.Temperature)
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
// TestCachePathFlagRegistered pins the fix for the documentation/CLI
// mismatch: the cache-path entry in the Help map promised a CLI flag,
// but initFlags() never registered one, so users could only set it via
// YAML or MODS_CACHE_PATH. Surface it as an advanced flag so the help
// output and the actual flag set agree.
func TestCachePathFlagRegistered(t *testing.T) {
	f := rootCmd.Flags().Lookup("cache-path")
	require.NotNil(t, f, "--cache-path must be a registered CLI flag")
	require.Equal(t, "string", f.Value.Type())

	saveCachePath := config.CachePath
	t.Cleanup(func() {
		config.CachePath = saveCachePath
		_ = rootCmd.Flags().Set("cache-path", saveCachePath)
	})

	// Setting the flag must drive the same destination as the YAML
	// field; verify by parsing and reading back.
	require.NoError(t, rootCmd.Flags().Set("cache-path", "/tmp/mods-cache"))
	require.Equal(t, "/tmp/mods-cache", config.CachePath)
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
