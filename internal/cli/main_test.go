package cli

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/panjie/mods/internal/proto"
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

		thinkFlag := rootCmd.Flags().Lookup("think")
		require.NotNil(t, thinkFlag)
		require.Equal(t, "t", thinkFlag.Shorthand)

		require.NoError(t, rootCmd.Flags().Parse([]string{"-t"}))
		require.True(t, config.Think)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--raw"}))
		require.True(t, config.Raw)
	})

	withTestConfig(t, Config{}, func() {
		require.Error(t, rootCmd.Flags().Parse([]string{"-T"}))
	})
}

func TestReviewModeFlagUsesClearNameAndRejectsOldName(t *testing.T) {
	flag := rootCmd.Flags().Lookup("review-mode")
	require.NotNil(t, flag)
	require.Equal(t, "V", flag.Shorthand)

	require.Nil(t, rootCmd.Flags().Lookup("review"))

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--review-mode", "auto"}))
		require.Equal(t, ReviewAuto, config.ReviewMode)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--review-mode", "always"}))
		require.Equal(t, ReviewAlways, config.ReviewMode)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"-V", "never"}))
		require.Equal(t, ReviewNever, config.ReviewMode)
	})

	withTestConfig(t, Config{}, func() {
		require.Error(t, rootCmd.Flags().Parse([]string{"--review", "auto"}))
		require.Error(t, rootCmd.Flags().Parse([]string{"--review", "mutable"}))
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

func TestShowToolResultsFlagRegistered(t *testing.T) {
	require.NotNil(t, rootCmd.Flags().Lookup("show-tool-results"))
}

func TestBuildTeaProgramOptionsMarksPipeReviewAvailableWhenStderrTTY(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	savedCanOpenReviewTTY := canOpenReviewTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
		canOpenReviewTTY = savedCanOpenReviewTTY
	})

	config = Config{}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }
	canOpenReviewTTY = func() bool { return true }

	_ = buildTeaProgramOptions()

	require.True(t, config.InteractiveReviewAvailable)
}

func TestBuildTeaProgramOptionsDoesNotMarkPipeReviewAvailableWhenTTYCannotOpen(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	savedCanOpenReviewTTY := canOpenReviewTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
		canOpenReviewTTY = savedCanOpenReviewTTY
	})

	config = Config{}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }
	canOpenReviewTTY = func() bool { return false }

	_ = buildTeaProgramOptions()

	require.False(t, config.InteractiveReviewAvailable)
}

func TestBuildTeaProgramOptionsDoesNotMarkTTYReviewAvailableWhenStderrIsNotTTY(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	savedCanOpenReviewTTY := canOpenReviewTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
		canOpenReviewTTY = savedCanOpenReviewTTY
	})

	config = Config{}
	IsInputTTY = func() bool { return true }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return false }
	canOpenReviewTTY = func() bool { return true }

	_ = buildTeaProgramOptions()

	require.False(t, config.InteractiveReviewAvailable)
}

func TestBuildTeaProgramOptionsDoesNotMarkRawPipeReviewAvailable(t *testing.T) {
	savedConfig := config
	savedIsInputTTY := IsInputTTY
	savedIsOutputTTY := IsOutputTTY
	savedIsErrorTTY := IsErrorTTY
	savedCanOpenReviewTTY := canOpenReviewTTY
	t.Cleanup(func() {
		config = savedConfig
		IsInputTTY = savedIsInputTTY
		IsOutputTTY = savedIsOutputTTY
		IsErrorTTY = savedIsErrorTTY
		canOpenReviewTTY = savedCanOpenReviewTTY
	})

	config = Config{PersistentConfig: PersistentConfig{Raw: true}}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }
	canOpenReviewTTY = func() bool { return true }

	_ = buildTeaProgramOptions()

	require.False(t, config.InteractiveReviewAvailable)
}

func TestShowTokenUsageFlagAndFormatting(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--show-token-usage"}))
		require.True(t, config.ShowTokenUsage)
	})
	require.Equal(t, "Token usage: unavailable", tokenUsageLine(proto.TokenUsage{}))
	require.Equal(t, "Token usage: input 7,735, output 9, total 7,744", tokenUsageLine(proto.TokenUsage{
		InputTokens: 7735, OutputTokens: 9, TotalTokens: 7744,
	}))
}

func TestFormatTokenCount(t *testing.T) {
	for _, test := range []struct {
		value int64
		want  string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1,000"},
		{7735, "7,735"},
		{123456789, "123,456,789"},
	} {
		require.Equal(t, test.want, formatTokenCount(test.value))
	}
}

func TestStyledTokenUsageLine(t *testing.T) {
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	styles := Styles{
		Comment: renderer.NewStyle().Foreground(lipgloss.Color("#757575")),
		Flag: renderer.NewStyle().
			Foreground(lipgloss.Color("#3EEFCF")).
			Bold(true),
	}

	got := styledTokenUsageLine(proto.TokenUsage{
		InputTokens: 7735, OutputTokens: 9, TotalTokens: 7744,
	}, styles)
	require.Equal(t, "  Tokens  7,735 input  ·  9 output  ·  7,744 total", ansi.Strip(got))
	require.NotEqual(t, ansi.Strip(got), got, "TTY status line must contain styling")
	require.Contains(t, got, styles.Flag.Render("7,735"))

	unavailable := styledTokenUsageLine(proto.TokenUsage{}, styles)
	require.Equal(t, "  Tokens  unavailable", ansi.Strip(unavailable))
	require.NotEqual(t, ansi.Strip(unavailable), unavailable)
}

func TestPrintTokenUsageNonTTYUsesOnlyStderr(t *testing.T) {
	savedConfig := config
	savedIsErrorTTY := IsErrorTTY
	defer func() {
		config = savedConfig
		IsErrorTTY = savedIsErrorTTY
	}()
	config.ShowTokenUsage = true
	IsErrorTTY = func() bool { return false }

	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			printTokenUsage(&Mods{})
		})
	})
	require.Empty(t, stdout)
	require.Equal(t, "Token usage: unavailable\n", stderr)
	require.NotContains(t, stderr, "\x1b[")
}

func TestPrintTokenUsageTTYAddsSpacingAndStyle(t *testing.T) {
	savedConfig := config
	savedIsErrorTTY := IsErrorTTY
	savedStderrStyles := StderrStyles
	defer func() {
		config = savedConfig
		IsErrorTTY = savedIsErrorTTY
		StderrStyles = savedStderrStyles
	}()
	config.ShowTokenUsage = true
	IsErrorTTY = func() bool { return true }
	renderer := lipgloss.NewRenderer(io.Discard)
	renderer.SetColorProfile(termenv.TrueColor)
	styles := Styles{
		Comment: renderer.NewStyle().Foreground(lipgloss.Color("#757575")),
		Flag: renderer.NewStyle().
			Foreground(lipgloss.Color("#3EEFCF")).
			Bold(true),
	}
	StderrStyles = func() Styles { return styles }

	var stderr string
	stdout := captureStdout(t, func() {
		stderr = captureStderr(t, func() {
			printTokenUsage(&Mods{})
		})
	})
	require.Empty(t, stdout)
	require.Equal(t, "\n  Tokens  unavailable\n", ansi.Strip(stderr))
	require.NotEqual(t, ansi.Strip(stderr), stderr)
}

func TestNoSaveFlag(t *testing.T) {
	flag := rootCmd.Flags().Lookup("no-save")
	require.NotNil(t, flag)
	require.Equal(t, "n", flag.Shorthand)

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"--no-save"}))
		require.True(t, config.NoSave)
	})

	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Parse([]string{"-n"}))
		require.True(t, config.NoSave)
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
	t.Run("with list skills", func(t *testing.T) {
		cfg := Config{ListSkills: true}
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
		!cfg.ListSkills &&
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

	for _, name := range []string{"word-wrap", "max-tool-rounds", "hide-tool-status"} {
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
	require.True(t, groupHasFlag(groups, flagCategoryToolsReview, "review-mode"))
	require.True(t, groupHasFlag(groups, flagCategoryConfigUI, "skills-dir"))
	require.True(t, groupHasFlag(groups, flagCategoryConfigUI, "list-skills"))
	require.False(t, groupHasFlag(groups, flagCategoryInputOutput, "hide-tool-results"))
	require.False(t, groupHasFlag(groups, flagCategoryToolsReview, "review"))

	for category, flags := range groups {
		require.False(t, groupHasFlag(map[string][]*pflag.Flag{category: flags}, category, "memprofile"))
	}
}

func TestListSkillsIsMutuallyExclusiveWithSessionActions(t *testing.T) {
	flag := rootCmd.Flags().Lookup(flagListSkills)
	require.NotNil(t, flag)
	groups := flag.Annotations["cobra_annotation_mutually_exclusive"]
	require.Len(t, groups, 1)
	require.Contains(t, groups[0], flagListPrompts)
	require.Contains(t, groups[0], flagListTools)
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

		require.Equal(t, 12, config.MaxToolRounds)
	})
}

func TestSkillsDirFlagOverridesConfig(t *testing.T) {
	withTestConfig(t, Config{PersistentConfig: PersistentConfig{SkillsDir: "/from-config"}}, func() {
		dir := filepath.Join(t.TempDir(), "skills")
		require.NoError(t, rootCmd.Flags().Set("skills-dir", dir))

		require.Equal(t, dir, config.SkillsDir)
	})
}

func TestReadmeDocumentsCurrentFlags(t *testing.T) {
	content, err := os.ReadFile(filepath.Join("..", "..", "README.md"))
	require.NoError(t, err)

	readme := string(content)
	require.Contains(t, readme, "[PROMPT...]")
	require.Contains(t, readme, "grouped by purpose")
	require.Contains(t, readme, "edit your files")
	require.Contains(t, readme, "run shell")
	require.Contains(t, readme, "review step")
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
