package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
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
		"mods --help-all":         false,
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

	require.True(t, config.InteractiveTTYAvailable)
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

	require.False(t, config.InteractiveTTYAvailable)
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

	require.False(t, config.InteractiveTTYAvailable)
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

	require.False(t, config.InteractiveTTYAvailable)
}

type quitImmediatelyModel struct{}

func (quitImmediatelyModel) Init() tea.Cmd { return tea.Quit }

func (m quitImmediatelyModel) Update(tea.Msg) (tea.Model, tea.Cmd) { return m, nil }

func (quitImmediatelyModel) View() tea.View { return tea.NewView("") }

func TestBuildTeaProgramOptionsDisablesRendererWithoutTerminalInput(t *testing.T) {
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

	t.Setenv("TERM", "xterm-ghostty")
	config = Config{}
	IsInputTTY = func() bool { return false }
	IsOutputTTY = func() bool { return true }
	IsErrorTTY = func() bool { return true }
	canOpenReviewTTY = func() bool { return false }

	output := captureStderr(t, func() {
		_, err := tea.NewProgram(quitImmediatelyModel{}, buildTeaProgramOptions()...).Run()
		require.NoError(t, err)
	})

	require.Empty(t, output)
	require.False(t, config.InteractiveTTYAvailable)
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
	styles := Styles{
		Comment: lipgloss.NewStyle().Foreground(lipgloss.Color("#757575")),
		Flag: lipgloss.NewStyle().
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
	styles := Styles{
		Comment: lipgloss.NewStyle().Foreground(lipgloss.Color("#757575")),
		Flag: lipgloss.NewStyle().
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
	require.Equal(t, ansi.Strip(stderr), stderr, "Lip Gloss writer must downgrade captured non-TTY output")
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

func TestHelpUsageShowsAllPublicFlags(t *testing.T) {
	flags := rootCmd.Flags()
	require.True(t, flagVisibleInUsage(flags.Lookup("model")))
	require.True(t, flagVisibleInUsage(flags.Lookup("workspace")))
	require.True(t, flagVisibleInUsage(flags.Lookup("word-wrap")))
	require.False(t, flagVisibleInUsage(flags.Lookup("memprofile")))
	require.Nil(t, flags.Lookup("help-all"))

	output := captureStdout(t, func() {
		require.NoError(t, usageFunc(rootCmd))
	})
	flags.VisitAll(func(f *pflag.Flag) {
		if f.Hidden {
			require.NotContains(t, output, "--"+f.Name)
			return
		}
		require.Contains(t, output, "--"+f.Name)
	})
	require.Contains(t, output, "[advanced]")
	require.NotContains(t, output, "--help-all")
}

func TestUsageIntroAndPromptSyntax(t *testing.T) {
	withTestConfig(t, Config{}, func() {
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

func TestHelpGroupsEveryPublicFlagInDeclaredOrder(t *testing.T) {
	groups := groupedUsageFlags(rootCmd.Flags())
	require.Empty(t, groups[flagCategoryOther])

	seen := make(map[string]int)
	for _, category := range flagCategorySpecs {
		flags := groups[category.Name]
		names := make([]string, 0, len(flags))
		for _, f := range flags {
			names = append(names, f.Name)
			seen[f.Name]++
			require.Equal(t, category.Name, flagCategory(f))
		}
		require.Equal(t, category.Flags, names, category.Name)
	}

	rootCmd.Flags().VisitAll(func(f *pflag.Flag) {
		if !f.Hidden {
			require.Equal(t, 1, seen[f.Name], f.Name)
		}
	})
}

func TestRegisteredFlagDescriptionsContainNoTerminalSequences(t *testing.T) {
	rootCmd.Flags().VisitAll(func(flag *pflag.Flag) {
		require.NotContains(t, flag.Usage, "\x1b", flag.Name)
	})
}

func TestHelpUsesWorkspaceAndReviewCategory(t *testing.T) {
	groups := groupedUsageFlags(rootCmd.Flags())
	require.True(t, groupHasFlag(groups, flagCategoryWorkspaceReview, "workspace"))
	require.True(t, groupHasFlag(groups, flagCategoryWorkspaceReview, "review-mode"))
	require.True(t, groupHasFlag(groups, flagCategoryToolsIntegrations, "skills-dirs"))
	require.True(t, groupHasFlag(groups, flagCategoryToolsIntegrations, "list-skills"))
	require.True(t, groupHasFlag(groups, flagCategoryToolsIntegrations, "list-tools"))
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
		require.Equal(t, fmt.Sprintf(
			"Configuration: %s\n     Sessions: %s\n",
			filepath.Dir(config.SettingsPath),
			config.SessionDir,
		), output)

		require.Error(t, runDirsAction([]string{"cache"}))
	})
}

func TestDirsBypassesBubbleTea(t *testing.T) {
	saveRunOneTurn := runOneTurnProgram
	defer func() { runOneTurnProgram = saveRunOneTurn }()

	called := false
	runOneTurnProgram = func(context.Context, []tea.ProgramOption) (*Mods, error) {
		called = true
		return nil, nil
	}

	withTestConfig(t, Config{Dirs: true, SettingsExisted: true}, func() {
		output := captureStdout(t, func() {
			require.NoError(t, rootCmd.RunE(rootCmd, nil))
		})
		require.Contains(t, output, "Configuration: ")
	})
	require.False(t, called)
}

func TestReadOnlyOneShotActionsBypassBubbleTea(t *testing.T) {
	tests := map[string]func(*Config){
		"dirs":          func(cfg *Config) { cfg.Dirs = true },
		"list roles":    func(cfg *Config) { cfg.ListRoles = true },
		"list prompts":  func(cfg *Config) { cfg.ListPrompts = true },
		"list skills":   func(cfg *Config) { cfg.ListSkills, cfg.Raw = true, true },
		"list sessions": func(cfg *Config) { cfg.List = true },
		"list MCPs":     func(cfg *Config) { cfg.MCPList = true },
		"list tools":    func(cfg *Config) { cfg.MCPListTools = true },
	}

	for name, configure := range tests {
		t.Run(name, func(t *testing.T) {
			saveRunOneTurn := runOneTurnProgram
			saveDB := db
			t.Cleanup(func() {
				runOneTurnProgram = saveRunOneTurn
				db = saveDB
			})

			cfg := Default()
			cfg.SettingsExisted = true
			cfg.SettingsPath = filepath.Join(t.TempDir(), "mods.yml")
			cfg.SessionDir = t.TempDir()
			cfg.SkillsDirs = []string{t.TempDir()}
			configure(&cfg)
			db = testDB(t)

			called := false
			runOneTurnProgram = func(context.Context, []tea.ProgramOption) (*Mods, error) {
				called = true
				return nil, nil
			}

			withTestConfig(t, cfg, func() {
				cmd := *rootCmd
				cmd.SetContext(context.Background())
				_ = captureStderr(t, func() {
					_ = captureStdout(t, func() {
						require.NoError(t, rootCmd.RunE(&cmd, nil))
					})
				})
			})
			require.False(t, called)
		})
	}
}

func TestAdvancedFlagsStillParse(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.NoError(t, rootCmd.Flags().Set("max-tool-rounds", "12"))

		require.Equal(t, 12, config.MaxToolRounds)
	})
}

func TestSkillsDirsFlagAppendsMultipleDirs(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.Nil(t, rootCmd.Flags().Lookup("skills-dir"))
		require.NotNil(t, rootCmd.Flags().Lookup("skills-dirs"))
		require.NoError(t, rootCmd.Flags().Set("skills-dirs", "/one"))
		require.NoError(t, rootCmd.Flags().Set("skills-dirs", "/two"))
		require.Equal(t, []string{"/one", "/two"}, config.SkillsDirs)
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
