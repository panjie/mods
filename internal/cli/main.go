// Package main provides the mods CLI.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	builddebug "runtime/debug"
	"runtime/pprof"
	"slices"
	"strings"

	"github.com/atotto/clipboard"
	timeago "github.com/caarlos0/timea.go"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	glamour "github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/x/editor"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"
)

// Build vars.
var (
	//nolint: gochecknoglobals
	Version   = ""
	CommitSHA = ""
)

func buildVersion() {
	if len(CommitSHA) >= ShortIDLength {
		vt := rootCmd.VersionTemplate()
		rootCmd.SetVersionTemplate(vt[:len(vt)-1] + " (" + CommitSHA[0:7] + ")\n")
	}
	if Version == "" {
		if info, ok := builddebug.ReadBuildInfo(); ok && info.Main.Sum != "" {
			Version = info.Main.Version
		} else {
			Version = "unknown (built from source)"
		}
	}
	rootCmd.Version = Version
}

func init() {
	// XXX: unset error Styles in Glamour dark and light Styles.
	// On the glamour side, we should probably add constructors for generating
	// default Styles so they can be essentially copied and altered without
	// mutating the definitions in Glamour itself (or relying on any deep
	// copying).
	glamour.DarkStyleConfig.CodeBlock.Chroma.Error.BackgroundColor = new(string)
	glamour.LightStyleConfig.CodeBlock.Chroma.Error.BackgroundColor = new(string)

	buildVersion()
	rootCmd.SetUsageFunc(usageFunc)
	rootCmd.SetHelpFunc(func(cmd *cobra.Command, _ []string) {
		_ = usageFunc(cmd)
	})
	rootCmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return newFlagParseError(err)
	})

	rootCmd.CompletionOptions.HiddenDefaultCmd = true
	rootCmd.SetHelpCommand(&cobra.Command{Hidden: true})
}

var (
	config = Default()
	db     *DB

	rootCmd = &cobra.Command{
		Use:           "mods",
		Short:         helpIntroSummary,
		SilenceUsage:  true,
		SilenceErrors: true,
		Example:       randomExample(),
		RunE: func(cmd *cobra.Command, args []string) error {
			debug.SetEnabled(config.Debug)
			config.Prefix = RemoveWhitespace(strings.Join(args, " "))

			if config.ShowHelp || config.HelpAll {
				return cmd.Usage()
			}

			opts := []tea.ProgramOption{}

			if !IsInputTTY() || config.Raw {
				opts = append(opts, tea.WithInput(nil))
			}
			if IsOutputTTY() && !config.Raw {
				opts = append(opts, tea.WithOutput(os.Stderr))
			} else {
				opts = append(opts, tea.WithoutRenderer())
			}
			if os.Getenv("VIMRUNTIME") != "" {
				config.Quiet = true
			}

			if isNoArgs() && IsInputTTY() && config.OpenEditor {
				prompt, err := prefixFromEditor()
				if err != nil {
					return err
				}
				config.Prefix = prompt
			}

			if (isNoArgs() || config.AskModel) && IsInputTTY() {
				if err := askInfo(); err != nil && err == huh.ErrUserAborted {
					return modsError{
						Err:        err,
						ReasonText: "User canceled.",
					}
				} else if err != nil {
					return modsError{
						Err:        err,
						ReasonText: "Prompt failed.",
					}
				}
			}

			if config.ConfigSetup {
				if err := RunConfigWizard(); err != nil {
					return modsError{Err: err, ReasonText: "Configuration wizard failed."}
				}
				return nil
			}

			if isNoArgs() && !HasAPIKey(&config) && config.API != "ollama" && IsErrorTTY() {
				fmt.Fprintf(os.Stderr, "\n  No API key detected for %s.\n  Run %s to configure your provider.\n\n",
					config.API, StderrStyles().InlineCode.Render("mods --config"))
			}

			mods, err := newMods(cmd.Context(), StderrRenderer(), &config, db)
			if err != nil {
				return modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
			}
			p := tea.NewProgram(mods, opts...)
			m, err := p.Run()
			if err != nil {
				return modsError{Err: err, ReasonText: "Couldn't start Bubble Tea program."}
			}

			mods = m.(*Mods)
			if mods.Error != nil {
				return *mods.Error
			}

			if config.Dirs {
				if len(args) > 0 {
					switch args[0] {
					case "config":
						fmt.Println(filepath.Dir(config.SettingsPath))
						return nil
					case "cache":
						fmt.Println(config.CachePath)
						return nil
					}
				}
				fmt.Printf("Configuration: %s\n", filepath.Dir(config.SettingsPath))
				//nolint:mnd
				fmt.Printf("%*sCache: %s\n", 8, " ", config.CachePath)
				return nil
			}

			if config.Settings {
				c, err := editor.Cmd("mods", config.SettingsPath)
				if err != nil {
					return modsError{
						Err:        err,
						ReasonText: "Could not edit your settings file.",
					}
				}
				HideCommandWindow(c)
				c.Stdin = os.Stdin
				c.Stdout = os.Stdout
				c.Stderr = os.Stderr
				if err := c.Run(); err != nil {
					return modsError{
						Err: err,
						ReasonText: fmt.Sprintf(
							"Missing %s.",
							StderrStyles().InlineCode.Render("$EDITOR"),
						),
					}
				}

				if !config.Quiet {
					fmt.Fprintln(os.Stderr, "Wrote config file to:", config.SettingsPath)
				}
				return nil
			}

			if config.ResetSettings {
				return resetSettings()
			}

			if mods.Input == "" && isNoArgs() {
				return modsError{
					ReasonText: "You haven't provided any prompt input.",
					Err: newUserErrorf(
						"You can give your prompt as arguments and/or pipe it from STDIN.\nExample: %s",
						StdoutStyles().InlineCode.Render("mods [PROMPT...]"),
					),
				}
			}

			if config.ListRoles {
				listRoles()
				return nil
			}
			if config.List {
				return listConversations(config.Raw)
			}

			if config.MCPList {
				List(&config)
				return nil
			}

			if config.MCPListTools {
				ctx, cancel := context.WithTimeout(cmd.Context(), config.MCPTimeout)
				defer cancel()
				return ListTools(ctx, &config)
			}

			if len(config.Delete) > 0 {
				return deleteConversations()
			}

			if config.DeleteOlderThan > 0 {
				return deleteConversationOlderThan()
			}

			// raw mode already prints the output, no need to print it again
			if IsOutputTTY() && !config.Raw {
				switch {
				case mods.RenderedOutput() != "":
					fmt.Print(mods.RenderedOutput())
				case mods.Output != "":
					fmt.Print(mods.Output)
				}
			}

			if config.Show != "" || config.ShowLast {
				return nil
			}

			if config.CacheWriteToID != "" {
				return saveConversation(mods)
			}

			return nil
		},
	}
)

var memprofile bool

func Run(version, commit string) int {
	Version = version
	CommitSHA = commit
	buildVersion()
	execute()
	return 0
}

func initFlags() {
	flags := rootCmd.Flags()
	regStr(flags, &config.Model, "model", "m", config.Model)
	regBool(flags, &config.AskModel, "ask-model", "M", config.AskModel)
	regStr(flags, &config.API, "api", "a", config.API)
	regStr(flags, &config.HTTPProxy, "http-proxy", "x", config.HTTPProxy)
	regBool(flags, &config.Format, "format", "f", config.Format)
	regStr(flags, &config.FormatAs, "format-as", "", config.FormatAs)
	regBool(flags, &config.Minimal, "minimal", "", config.Minimal)
	regBool(flags, &config.Raw, "raw", "r", config.Raw)
	regStr(flags, &config.Continue, "continue", "c", "")
	regBool(flags, &config.ContinueLast, "continue-last", "C", false)
	regBool(flags, &config.List, "list", "l", config.List)
	regStr(flags, &config.Title, "title", "t", config.Title)
	regStrArr(flags, &config.Delete, "delete", "d", config.Delete)
	flags.Var(newDurationFlag(config.DeleteOlderThan, &config.DeleteOlderThan), flagDeleteOlder, flagDesc(flagDeleteOlder))
	regStr(flags, &config.Show, "show", "s", config.Show)
	regBool(flags, &config.ShowLast, "show-last", "S", false)
	regBool(flags, &config.Quiet, "quiet", "q", config.Quiet)
	regBool(flags, &config.HideToolStatus, "hide-tool-status", "", config.HideToolStatus)
	regBool(flags, &config.ShowHelp, "help", "h", false)
	regBool(flags, &config.HelpAll, "help-all", "", false)
	regBool(flags, &config.Version, "version", "v", false)
	regInt(flags, &config.MaxRetries, "max-retries", config.MaxRetries)
	regBool(flags, &config.NoLimit, "no-limit", "", config.NoLimit)
	regInt64(flags, &config.MaxTokens, "max-tokens", config.MaxTokens)
	regInt(flags, &config.WordWrap, "word-wrap", config.WordWrap)
	regStr(flags, &config.BuiltinTools.WorkspaceRoot, "workspace", "", config.BuiltinTools.WorkspaceRoot)
	regFloat64(flags, &config.Temperature, "temp", config.Temperature)
	regStrArr(flags, &config.Stop, "stop", "", config.Stop)
	regFloat64(flags, &config.TopP, "topp", config.TopP)
	regInt64(flags, &config.TopK, "topk", config.TopK)
	regStr(flags, &config.StatusText, "status-text", "", config.StatusText)
	regBool(flags, &config.NoCache, "no-cache", "", config.NoCache)
	regBool(flags, &config.ResetSettings, "reset-settings", "", config.ResetSettings)
	regBool(flags, &config.Settings, "settings", "", false)
	regBool(flags, &config.ConfigSetup, "config", "", false)
	regBool(flags, &config.Dirs, "dirs", "", false)
	regStr(flags, &config.Role, "role", "R", config.Role)
	regBool(flags, &config.ListRoles, "list-roles", "", config.ListRoles)
	regStr(flags, &config.Theme, "theme", "", config.Theme)
	regBool(flags, &config.OpenEditor, "editor", "e", false)
	regBool(flags, &config.Plan, "plan", "p", config.Plan)
	regBool(flags, &config.MCPList, "mcp-list", "", false)
	regBool(flags, &config.MCPListTools, "mcp-list-tools", "", false)
	regStrArr(flags, &config.MCPDisable, "mcp-disable", "", nil)
	regStrArr(flags, &config.MCPEnable, "mcp-enable", "", nil)
	regBool(flags, &config.WebSearch, "web-search", "", config.WebSearch)
	regStr(flags, &config.WebSearchProvider, "web-search-provider", "", config.WebSearchProvider)
	regStr(flags, &config.WebSearchAPIKey, "web-search-api-key", "", config.WebSearchAPIKey)
	regStrArr(flags, &config.Images, "image", "i", config.Images)
	regBool(flags, &config.StdinImage, "stdin-image", "", config.StdinImage)
	regBool(flags, &config.ClipboardImage, "clipboard-image", "", config.ClipboardImage)
	regBool(flags, &config.Debug, "debug", "D", config.Debug)
	regInt(flags, &config.MaxToolRounds, "max-tool-rounds", config.MaxToolRounds)
	f := flags.VarPF(newReasoningFlag(config.Reasoning, &config.Reasoning), "reasoning", "T", flagDesc("reasoning"))
	f.NoOptDefVal = "on"
	flags.VarP(newReviewFlag(config.ReviewMode, &config.ReviewMode), "review", "V", flagDesc("review"))

	flags.BoolVar(&memprofile, "memprofile", false, "Write memory profiles to CWD")
	_ = flags.MarkHidden("memprofile")
	markAdvanced(
		flags,
		"http-proxy",
		"max-retries",
		"max-tokens",
		"no-limit",
		"temp",
		"topp",
		"topk",
		"stop",
		"word-wrap",
		"status-text",
		"theme",
		"hide-tool-status",
		"web-search-provider",
		"web-search-api-key",
		"max-tool-rounds",
		"mcp-enable",
		"mcp-disable",
		"mcp-list",
		"mcp-list-tools",
		"debug",
		"stdin-image",
		"clipboard-image",
		"format-as",
		"no-cache",
	)
	markCategory(flags, flagCategoryModelAPI, "api", "model", "ask-model", "http-proxy")
	markCategory(
		flags,
		flagCategorySession,
		"title",
		"list",
		"continue",
		"continue-last",
		"show",
		"show-last",
		"delete",
		flagDeleteOlder,
		"no-cache",
	)
	markCategory(
		flags,
		flagCategoryInputOutput,
		"format",
		"format-as",
		"minimal",
		"raw",
		"quiet",
		"hide-tool-status",
		"word-wrap",
		"status-text",
		"workspace",
		"editor",
		"image",
		"stdin-image",
		"clipboard-image",
	)
	markCategory(flags, flagCategoryConfigUI, "settings", "config", "dirs", "reset-settings", "theme", "help", "help-all", "version")
	markCategory(flags, flagCategoryRoles, "role", "list-roles")
	markCategory(flags, flagCategoryWebSearch, "web-search", "web-search-provider", "web-search-api-key")
	markCategory(flags, flagCategoryToolsReview, "plan", "reasoning", "review", "max-tool-rounds")
	markCategory(flags, flagCategoryMCP, "mcp-list", "mcp-list-tools", "mcp-enable", "mcp-disable")
	markCategory(
		flags,
		flagCategoryModelParams,
		"max-retries",
		"max-tokens",
		"max-input-chars",
		"no-limit",
		"temp",
		"topp",
		"topk",
		"stop",
	)
	markCategory(flags, flagCategoryDebug, "debug")

	for _, name := range conversationCompleteFlags {
		_ = rootCmd.RegisterFlagCompletionFunc(name, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return conversationCompletions(toComplete), cobra.ShellCompDirectiveDefault
		})
	}
	_ = rootCmd.RegisterFlagCompletionFunc("role", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return roleNames(toComplete), cobra.ShellCompDirectiveDefault
	})

	if config.FormatText == nil {
		config.FormatText = Default().FormatText
	}

	if config.Format && config.FormatAs == "" {
		config.FormatAs = "markdown"
	}

	if config.Format && config.FormatAs != "" && config.FormatText[config.FormatAs] == "" {
		config.FormatText[config.FormatAs] = Default().FormatText[config.FormatAs]
	}

	if config.MCPTimeout == 0 {
		config.MCPTimeout = Default().MCPTimeout
	}

	rootCmd.MarkFlagsMutuallyExclusive(sessionActionFlags...)
}

func conversationCompletions(toComplete string) []string {
	var err error
	if db == nil {
		db, err = Open(filepath.Join(config.CachePath, "conversations", "mods.db"))
		if err != nil {
			return nil
		}
	}

	results, err := db.Completions(toComplete)
	if err != nil {
		return nil
	}
	return results
}

func execute() {
	defer maybeWriteMemProfile()
	var err error
	config, err = Ensure()
	if err != nil {
		handleError(modsError{Err: err, ReasonText: "Could not load your configuration file."})
		if !slices.Contains(os.Args, "--settings") && !slices.Contains(os.Args, "--config") {
			os.Exit(1)
		}
	}

	debug.Printf("Config loaded from: %s", config.SettingsPath)
	debug.Printf("API: %s, Model: %s", config.API, config.Model)
	debug.Printf("Role: %s, Format: %v, Format-as: %s, Raw: %v, Quiet: %v", config.Role, config.Format, config.FormatAs, config.Raw, config.Quiet)
	debug.Printf("Cache path: %s", config.CachePath)

	// XXX: this must come after creating the config.
	initFlags()

	if !isCompletionCmd(os.Args) && !isVersionOrHelpCmd(os.Args) {
		db, err = Open(filepath.Join(config.CachePath, "conversations", "mods.db"))
		if err != nil {
			handleError(modsError{Err: err, ReasonText: "Could not open database."})
			os.Exit(1)
		}
		defer db.Close() //nolint:errcheck
		if err := db.MigrateLegacyConversations(config.CachePath); err != nil {
			fmt.Fprintln(os.Stderr, "Warning: some legacy conversations were not migrated:")
			fmt.Fprintln(os.Stderr, err)
		}
	}

	if isCompletionCmd(os.Args) {
		// XXX: since mods doesn't have any sub-commands, Cobra won't create
		// the default `completion` command. Forcefully create the completion
		// related sub-commands by adding a fake command when completions are
		// being used.
		rootCmd.AddCommand(&cobra.Command{
			Use:    "____fake_command_to_enable_completions",
			Hidden: true,
		})
		rootCmd.InitDefaultCompletionCmd()
	}

	if err := rootCmd.Execute(); err != nil {
		handleError(err)
		if db != nil {
			_ = db.Close()
		}
		os.Exit(1)
	}
}

func maybeWriteMemProfile() {
	if !memprofile {
		return
	}

	var closers []func() error
	if db != nil {
		closers = append(closers, db.Close)
	}
	defer func() {
		for _, cl := range closers {
			_ = cl()
		}
	}()

	handle := func(err error) {
		fmt.Println(err)
		for _, cl := range closers {
			_ = cl()
		}
		os.Exit(1)
	}

	heap, err := os.Create("mods_heap.profile")
	if err != nil {
		handle(err)
	}
	closers = append(closers, heap.Close)
	allocs, err := os.Create("mods_allocs.profile")
	if err != nil {
		handle(err)
	}
	closers = append(closers, allocs.Close)

	if err := pprof.Lookup("heap").WriteTo(heap, 0); err != nil {
		handle(err)
	}
	if err := pprof.Lookup("allocs").WriteTo(allocs, 0); err != nil {
		handle(err)
	}
}

func handleError(err error) {
	maybeWriteMemProfile()
	// exhaust stdin
	if !IsInputTTY() {
		_, _ = io.ReadAll(os.Stdin)
	}

	format := "\n%s\n\n"

	var args []any
	var ferr flagParseError
	var merr modsError
	if errors.As(err, &ferr) {
		format += "%s\n\n"
		args = []any{
			fmt.Sprintf(
				"Check out %s %s",
				StderrStyles().InlineCode.Render("mods -h"),
				StderrStyles().Comment.Render("for help."),
			),
			fmt.Sprintf(
				ferr.ReasonFormat(),
				StderrStyles().InlineCode.Render(ferr.Flag()),
			),
		}
	} else if errors.As(err, &merr) {
		args = []any{
			StderrStyles().ErrPadding.Render(StderrStyles().ErrorHeader.String(), merr.ReasonText),
		}

		// Skip the error details if the user simply canceled out of huh.
		if merr.Err != huh.ErrUserAborted {
			format += "%s\n\n"
			args = append(args, StderrStyles().ErrPadding.Render(StderrStyles().ErrorDetails.Render(err.Error())))
		}
	} else {
		args = []any{
			StderrStyles().ErrPadding.Render(StderrStyles().ErrorDetails.Render(err.Error())),
		}
	}

	fmt.Fprintf(os.Stderr, format, args...)
}

func resetSettings() error {
	_, err := os.Stat(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't read config file."}
	}
	inputFile, err := os.Open(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't open config file."}
	}
	defer inputFile.Close() //nolint:errcheck
	outputFile, err := os.Create(config.SettingsPath + ".bak")
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't backup config file."}
	}
	defer outputFile.Close() //nolint:errcheck
	_, err = io.Copy(outputFile, inputFile)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't write config file."}
	}
	// The copy was successful, so now delete the original file
	inputFile.Close()
	outputFile.Close()
	err = os.Remove(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't remove config file."}
	}
	err = WriteDefaultFile(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't write new config file."}
	}
	if !config.Quiet {
		fmt.Fprintln(os.Stderr, "\nSettings restored to defaults!")
		fmt.Fprintf(os.Stderr,
			"\n  %s %s\n\n",
			StderrStyles().Comment.Render("Your old settings have been saved to:"),
			StderrStyles().Link.Render(config.SettingsPath+".bak"),
		)
	}
	return nil
}

func deleteConversationOlderThan() error {
	conversations, err := db.ListOlderThan(config.DeleteOlderThan)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't find conversation to delete."}
	}

	if len(conversations) == 0 {
		if !config.Quiet {
			fmt.Fprintln(os.Stderr, "No conversations found.")
			return nil
		}
		return nil
	}

	if !config.Quiet {
		printList(conversations)

		if !IsOutputTTY() || !IsInputTTY() {
			fmt.Fprintln(os.Stderr)
			return newUserErrorf(
				"To delete the conversations above, run: %s",
				strings.Join(append(os.Args, "--quiet"), " "),
			)
		}
		var confirm bool
		if err := huh.Run(
			huh.NewConfirm().
				Title(fmt.Sprintf("Delete conversations older than %s?", config.DeleteOlderThan)).
				Description(fmt.Sprintf("This will delete all the %d conversations listed above.", len(conversations))).
				Value(&confirm),
		); err != nil {
			return modsError{Err: err, ReasonText: "Couldn't delete old conversations."}
		}
		if !confirm {
			return newUserErrorf("Aborted by user")
		}
	}

	for _, c := range conversations {
		if err := db.Delete(c.ID); err != nil {
			return modsError{Err: err, ReasonText: "Couldn't delete conversation."}
		}
		if err := removeLegacyConversationFile(c.ID); err != nil {
			return modsError{Err: err, ReasonText: "Couldn't delete legacy conversation data."}
		}

		if !config.Quiet {
			fmt.Fprintln(os.Stderr, "Conversation deleted:", c.ID[:MinIDLength])
		}
	}

	return nil
}

func deleteConversations() error {
	for _, del := range config.Delete {
		convo, err := db.Find(del)
		if err != nil {
			return modsError{Err: err, ReasonText: "Couldn't find conversation to delete."}
		}
		if err := deleteConversation(convo); err != nil {
			return err
		}
	}
	return nil
}

func deleteConversation(convo *Conversation) error {
	if err := db.Delete(convo.ID); err != nil {
		return modsError{Err: err, ReasonText: "Couldn't delete conversation."}
	}
	if err := removeLegacyConversationFile(convo.ID); err != nil {
		return modsError{Err: err, ReasonText: "Couldn't delete legacy conversation data."}
	}

	if !config.Quiet {
		fmt.Fprintln(os.Stderr, "Conversation deleted:", convo.ID[:MinIDLength])
	}
	return nil
}

func removeLegacyConversationFile(id string) error {
	path := filepath.Join(config.CachePath, "conversations", id+".gob")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func listConversations(raw bool) error {
	conversations, err := db.List()
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't list saves."}
	}

	if len(conversations) == 0 {
		fmt.Fprintln(os.Stderr, "No conversations found.")
		return nil
	}

	if IsInputTTY() && IsOutputTTY() && !raw {
		selectFromList(conversations)
		return nil
	}
	printList(conversations)
	return nil
}

func roleNames(prefix string) []string {
	roles := make([]string, 0, len(config.Roles))
	for role := range config.Roles {
		if prefix != "" && !strings.HasPrefix(role, prefix) {
			continue
		}
		roles = append(roles, role)
	}
	slices.Sort(roles)
	return roles
}

func listRoles() {
	for _, role := range roleNames("") {
		s := role
		if role == config.Role {
			s = role + StdoutStyles().Timeago.Render(" (default)")
		}
		fmt.Println(s)
	}
}

func makeOptions(conversations []Conversation) []huh.Option[string] {
	opts := make([]huh.Option[string], 0, len(conversations))
	for _, c := range conversations {
		timea := StdoutStyles().Timeago.Render(timeago.Of(c.UpdatedAt))
		left := StdoutStyles().ShaHash.Render(c.ID[:ShortIDLength])
		right := StdoutStyles().ConversationList.Render(c.Title, timea)
		if c.Model != nil {
			right += StdoutStyles().Comment.Render(*c.Model)
		}
		if c.API != nil {
			right += StdoutStyles().Comment.Render(" (" + *c.API + ")")
		}
		opts = append(opts, huh.NewOption(left+" "+right, c.ID))
	}
	return opts
}

func selectFromList(conversations []Conversation) {
	var selected string
	if err := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Conversations").
				Value(&selected).
				Options(makeOptions(conversations)...),
		),
	).Run(); err != nil {
		if !errors.Is(err, huh.ErrUserAborted) {
			fmt.Fprintln(os.Stderr, err.Error())
		}
		return
	}

	_ = clipboard.WriteAll(selected)
	termenv.Copy(selected)
	PrintConfirmation("COPIED", selected)
	// suggest actions to use this conversation ID
	fmt.Println(StdoutStyles().Comment.Render(
		"You can use this conversation ID with the following commands:",
	))
	for _, flag := range conversationCompleteFlags {
		fmt.Printf(
			"  %-44s %s\n",
			StdoutStyles().Flag.Render("--"+flag),
			StdoutStyles().FlagDesc.Render(Help[flag]),
		)
	}
}

func printList(conversations []Conversation) {
	for _, conversation := range conversations {
		_, _ = fmt.Fprintf(
			os.Stdout,
			"%s\t%s\t%s\n",
			StdoutStyles().ShaHash.Render(conversation.ID[:ShortIDLength]),
			conversation.Title,
			StdoutStyles().Timeago.Render(timeago.Of(conversation.UpdatedAt)),
		)
	}
}

func saveConversation(mods *Mods) error {
	if config.NoCache {
		if !config.Quiet {
			fmt.Fprintf(
				os.Stderr,
				"\nConversation was not saved because %s or %s is set.\n",
				StderrStyles().InlineCode.Render("--no-cache"),
				StderrStyles().InlineCode.Render("NO_CACHE"),
			)
		}
		return nil
	}

	// if message is a sha1, use the last prompt instead.
	id := config.CacheWriteToID
	title := strings.TrimSpace(config.CacheWriteToTitle)

	if IDPattern.MatchString(title) || title == "" {
		title = FirstLine(lastPrompt(mods.Messages()))
	}

	errReason := fmt.Sprintf(
		"There was a problem saving conversation %s. Use %s / %s to disable persistence.",
		config.CacheWriteToID,
		StderrStyles().InlineCode.Render("--no-cache"),
		StderrStyles().InlineCode.Render("NO_CACHE"),
	)
	if err := db.SaveConversation(
		id,
		title,
		config.API,
		config.Model,
		mods.Messages(),
		mods.ApprovalRules(),
	); err != nil {
		return modsError{Err: err, ReasonText: errReason}
	}

	if !config.Quiet {
		fmt.Fprintln(
			os.Stderr,
			"\nConversation saved:",
			StderrStyles().InlineCode.Render(config.CacheWriteToID[:ShortIDLength]),
			StderrStyles().Comment.Render(title),
		)
	}
	return nil
}

// isNoArgs reports whether the invocation is effectively empty (no prompt and
// no side-effect action). It deliberately checks Config fields directly rather
// than scanning sessionActionFlags: it must also consider ListRoles, Dirs and
// ShowHelp, which are NOT part of the mutually-exclusive action set. Keep this
// in sync with sessionActionFlags when adding a new session action.
func isNoArgs() bool {
	return config.Prefix == "" &&
		config.Show == "" &&
		!config.ShowLast &&
		len(config.Delete) == 0 &&
		config.DeleteOlderThan == 0 &&
		!config.ShowHelp &&
		!config.HelpAll &&
		!config.List &&
		!config.ListRoles &&
		!config.MCPList &&
		!config.MCPListTools &&
		!config.Dirs &&
		!config.Settings &&
		!config.ConfigSetup &&
		!config.ResetSettings
}

func askInfo() error {
	var foundModel bool
	apis := make([]huh.Option[string], 0, len(config.APIs))
	opts := map[string][]huh.Option[string]{}
	for _, api := range config.APIs {
		apis = append(apis, huh.NewOption(api.Name, api.Name))
		for name, model := range api.Models {
			opts[api.Name] = append(opts[api.Name], huh.NewOption(name, name))

			// checks if this is the model we intend to use if not using
			// `--ask-model`:
			if !config.AskModel &&
				(config.API == "" || config.API == api.Name) &&
				(config.Model == name || slices.Contains(model.Aliases, config.Model)) {
				// if it is, adjusts api and model so its cheaper later on.
				config.API = api.Name
				config.Model = name
				foundModel = true
			}
		}
	}

	if config.ContinueLast {
		found, err := db.FindHEAD()
		if err == nil && found != nil && found.Model != nil && found.API != nil {
			config.Model = *found.Model
			config.API = *found.API
			foundModel = true
		}
	}

	keymap := huh.NewDefaultKeyMap()
	keymap.Text.NewLine = key.NewBinding(
		key.WithKeys("ctrl+j"),
		key.WithHelp("ctrl+j", "new line"),
	)

	// wrapping is done by the caller
	//nolint:wrapcheck
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Choose the API:").
				Options(apis...).
				Value(&config.API),
			huh.NewSelect[string]().
				TitleFunc(func() string {
					return fmt.Sprintf("Choose the model for '%s':", config.API)
				}, &config.API).
				OptionsFunc(func() []huh.Option[string] {
					return opts[config.API]
				}, &config.API).
				Value(&config.Model),
		).WithHideFunc(func() bool {
			// AskModel is true if the user is passing a flag to ask;
			// FoundModel is true if a model is found for whatever config the
			// user has (either --api/--model or default-api and
			// default-model in settings).
			// So, it'll only hide this if the user didn't run with
			// `--ask-model` AND the configuration yields a valid model.
			return !config.AskModel && foundModel
		}),
		huh.NewGroup(
			huh.NewText().
				TitleFunc(func() string {
					return fmt.Sprintf("Enter a prompt for %s/%s:", config.API, config.Model)
				}, &config.Model).
				Value(&config.Prefix),
		).WithHideFunc(func() bool {
			return config.Prefix != ""
		}),
	).
		WithTheme(themeFrom(config.Theme)).
		WithKeyMap(keymap).
		Run()
}

//nolint:mnd
func isCompletionCmd(args []string) bool {
	if len(args) <= 1 {
		return false
	}
	if args[1] == "__complete" {
		return true
	}
	if args[1] != "completion" {
		return false
	}
	if len(args) == 3 {
		_, ok := map[string]any{
			"bash":       nil,
			"fish":       nil,
			"zsh":        nil,
			"powershell": nil,
			"-h":         nil,
			"--help":     nil,
			"help":       nil,
		}[args[2]]
		return ok
	}
	if len(args) == 4 {
		_, ok := map[string]any{
			"-h":     nil,
			"--help": nil,
		}[args[3]]
		return ok
	}
	return false
}

//nolint:mnd
func isVersionOrHelpCmd(args []string) bool {
	if len(args) <= 1 {
		return false
	}
	for _, arg := range args[1:] {
		if arg == "--version" || arg == "-v" || arg == "--help" || arg == "-h" || arg == "--help-all" {
			return true
		}
	}
	return false
}

func themeFrom(theme string) *huh.Theme {
	switch theme {
	case "dracula":
		return huh.ThemeDracula()
	case "catppuccin":
		return huh.ThemeCatppuccin()
	case "base16":
		return huh.ThemeBase16()
	default:
		return huh.ThemeCharm()
	}
}

// creates a temp file, opens it in user's editor, and then returns its contents.
func prefixFromEditor() (string, error) {
	f, err := os.CreateTemp("", "prompt")
	if err != nil {
		return "", fmt.Errorf("could not create temporary file: %w", err)
	}
	_ = f.Close()
	defer func() { _ = os.Remove(f.Name()) }()
	cmd, err := editor.Cmd(
		"mods",
		f.Name(),
	)
	if err != nil {
		return "", fmt.Errorf("could not open editor: %w", err)
	}
	HideCommandWindow(cmd)
	cmd.Stdin = os.Stdin
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("could not open editor: %w", err)
	}
	prompt, err := os.ReadFile(f.Name())
	if err != nil {
		return "", fmt.Errorf("could not read file: %w", err)
	}
	return string(prompt), nil
}
