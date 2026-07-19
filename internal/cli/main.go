// Package main provides the mods CLI.
package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	builddebug "runtime/debug"
	"runtime/pprof"
	"slices"
	"strings"

	glamour "charm.land/glamour/v2/styles"
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	timeago "github.com/caarlos0/timea.go"
	"github.com/charmbracelet/x/editor"
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

	runOneTurnProgram = runOneTurn

	rootCmd = &cobra.Command{
		Use:           "mods",
		Short:         helpIntroSummary,
		SilenceUsage:  true,
		SilenceErrors: true,
		Example:       randomExample(),
		RunE: func(cmd *cobra.Command, args []string) error {
			debug.SetEnabled(config.Debug)
			config.Prefix = RemoveWhitespace(strings.Join(args, " "))

			if config.ShowHelp {
				return cmd.Usage()
			}

			if autoConfig, err := maybeRunAutoConfig(os.Args); autoConfig || err != nil {
				return err
			}

			if err := validateFirstRunPrerequisites(os.Args); err != nil {
				return err
			}

			if config.ConfigSetup {
				if err := runConfigWizard(); err != nil {
					return modsError{Err: err, ReasonText: "Configuration wizard failed."}
				}
				return nil
			}

			if handled, err := dispatchPreTurnAction(cmd.Context(), args); handled {
				return err
			}

			if err := gatherInteractivePrompt(); err != nil {
				return err
			}

			maybePrintMissingAPIKeyHint()

			if config.Chat {
				opts := buildTeaProgramOptions()
				return runChat(cmd.Context(), args, opts)
			}

			opts := buildTeaProgramOptions()
			mods, err := runOneTurnProgram(cmd.Context(), opts)
			if err != nil {
				return err
			}

			return dispatchTurnResult(mods)
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
	fF := flags.VarPF(newFormatFlag(config.Format, &config.Format), "format", "f", flagDesc("format"))
	fF.NoOptDefVal = "markdown"
	regBool(flags, &config.Minimal, "minimal", "", config.Minimal)
	regBool(flags, &config.Raw, "raw", "", config.Raw)
	regStr(flags, &config.Continue, "continue", "C", "")
	regBool(flags, &config.ContinueLast, "continue-last", "c", false)
	regBool(flags, &config.List, flagListSessions, "l", config.List)
	regBool(flags, &config.Chat, flagChat, "", false)
	regBool(flags, &config.HideToolStatus, "hide-tool-status", "", config.HideToolStatus)
	regBool(flags, &config.ShowTokenUsage, "show-token-usage", "", config.ShowTokenUsage)
	regBool(flags, &config.ShowHelp, "help", "h", false)
	regBool(flags, &config.Version, "version", "v", false)
	regInt(flags, &config.MaxRetries, "max-retries", config.MaxRetries)
	regBool(flags, &config.NoLimit, "no-limit", "", config.NoLimit)
	regInt64(flags, &config.MaxTokens, "max-tokens", config.MaxTokens)
	regInt(flags, &config.WordWrap, "word-wrap", config.WordWrap)
	regStr(flags, &config.BuiltinTools.Workspace, "workspace", "", config.BuiltinTools.Workspace)
	regStrArr(flags, &config.SkillsDirs, "skills-dirs", "", config.SkillsDirs)
	regBool(flags, &config.NoSave, "no-save", "n", config.NoSave)
	regBool(flags, &config.NoInstructions, "no-instructions", "", config.NoInstructions)
	regBool(flags, &config.ResetSettings, "reset-settings", "", config.ResetSettings)
	regBool(flags, &config.Settings, "settings", "", false)
	regBool(flags, &config.ConfigSetup, "config", "", false)
	regBool(flags, &config.Dirs, "dirs", "", false)
	regStr(flags, &config.Role, "role", "R", config.Role)
	regBool(flags, &config.ListRoles, "list-roles", "", config.ListRoles)
	regBool(flags, &config.ListPrompts, flagListPrompts, "", config.ListPrompts)
	regBool(flags, &config.ListSkills, flagListSkills, "", config.ListSkills)
	regBool(flags, &config.OpenEditor, "editor", "e", false)
	regBool(flags, &config.Plan, "plan", "p", config.Plan)
	regBool(flags, &config.MCPList, "list-mcps", "", false)
	regBool(flags, &config.MCPListTools, "list-tools", "", false)

	regBool(flags, &config.WebSearch, "web-search", "", config.WebSearch)
	regStrArr(flags, &config.Images, "image", "i", config.Images)
	regBool(flags, &config.StdinImage, "stdin-image", "", config.StdinImage)
	regBool(flags, &config.ClipboardImage, "clipboard-image", "I", config.ClipboardImage)
	regBool(flags, &config.Debug, "debug", "D", config.Debug)
	regInt(flags, &config.MaxToolRounds, "max-tool-rounds", config.MaxToolRounds)
	regBool(flags, &config.Think, "think", "t", config.Think)
	flags.VarP(newReviewFlag(config.ReviewMode, &config.ReviewMode), "review-mode", "V", flagDesc("review-mode"))

	flags.BoolVar(&memprofile, "memprofile", false, "Write memory profiles to CWD")
	_ = flags.MarkHidden("memprofile")
	markAdvanced(
		flags,
		"http-proxy",
		"max-retries",
		"max-tokens",
		"no-limit",
		"word-wrap",
		"hide-tool-status",
		"show-token-usage",
		"max-tool-rounds",
		"list-mcps",
		"list-tools",
		"debug",
		"stdin-image",
		"clipboard-image",
		"no-save",
		"no-instructions",
	)
	applyFlagCategories(flags)
	registeredSelfHelpFlags = selfHelpFlagGroups(flags)

	for _, name := range sessionCompleteFlags {
		_ = rootCmd.RegisterFlagCompletionFunc(name, func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			return sessionCompletions(toComplete), cobra.ShellCompDirectiveDefault
		})
	}
	_ = rootCmd.RegisterFlagCompletionFunc("role", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		return roleNames(toComplete), cobra.ShellCompDirectiveDefault
	})

	// Default-value normalization (WordWrap, MCPTimeout, FormatText,
	// Format, WebSearchAPIKeyEnv, WebSearchAPIKey) is performed once in
	// Config.applyDefaults via Ensure(). The CLI flag defaults are
	// registered from the already-normalized config below, so they
	// inherit those canonical values without re-deriving them here.

	rootCmd.MarkFlagsMutuallyExclusive(sessionActionFlags...)
}

func sessionCompletions(toComplete string) []string {
	// Cobra invokes flag completions via the __complete subcommand on
	// every shell tab, so the package-level db may already be opened by
	// execute() and we want to reuse it. When called in completion-only
	// mode (or by tests that haven't set db), open a private connection
	// for the duration of the call and close it before returning, so a
	// completion invocation never leaks a dangling DB handle through
	// the package-level variable.
	completionDB := db
	if completionDB == nil {
		if config.SessionDir == "" {
			return nil
		}
		var err error
		if err := MigrateDefaultStorage(config.SessionDir); err != nil {
			return nil
		}
		completionDB, err = Open(filepath.Join(config.SessionDir, "mods.db"))
		if err != nil {
			return nil
		}
		defer completionDB.Close() //nolint:errcheck
	}

	results, err := completionDB.Completions(toComplete)
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
	debug.Printf("Role: %s, Format: %s, Raw: %v", config.Role, config.Format, config.Raw)
	debug.Printf("Session dir: %s", config.SessionDir)
	if config.PortableDir != "" {
		debug.Printf("Portable mode: %s", config.PortableDir)
	}

	// XXX: this must come after creating the config.
	initFlags()

	if !isCompletionCmd(os.Args) && !isVersionOrHelpCmd(os.Args) {
		if err := MigrateDefaultStorage(config.SessionDir); err != nil {
			handleError(modsError{Err: err, ReasonText: "Could not migrate session storage."})
			os.Exit(1)
		}
		db, err = Open(filepath.Join(config.SessionDir, "mods.db"))
		if err != nil {
			handleError(modsError{Err: err, ReasonText: "Could not open database."})
			os.Exit(1)
		}
		defer db.Close() //nolint:errcheck
		if err := db.MigrateLegacySessions(config.SessionDir); err != nil {
			fmt.Fprintln(os.Stderr, "Warning: some legacy sessions were not migrated:")
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

	rootCmd.SetArgs(os.Args[1:])

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
		// Render only the inner err message so the ReasonText (already
		// shown in the header above) is not repeated by Error.Error().
		if merr.Err != nil && merr.Err != huh.ErrUserAborted {
			format += "%s\n\n"
			args = append(args, StderrStyles().ErrPadding.Render(StderrStyles().ErrorDetails.Render(merr.Err.Error())))
		}
	} else {
		args = []any{
			StderrStyles().ErrPadding.Render(StderrStyles().ErrorDetails.Render(err.Error())),
		}
	}

	_, _ = lipgloss.Fprintf(os.Stderr, format, args...)
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

	// Pick a backup name that does not silently overwrite an existing
	// .bak: the original config can contain plaintext API keys, so a
	// previous reset's backup must not be clobbered. If foo.bak exists,
	// fall back to foo.bak.1, foo.bak.2, ... until a free slot is found.
	backupPath, err := nextBackupPath(config.SettingsPath + ".bak")
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't pick a backup file name."}
	}
	// Create the backup with the same restrictive mode the original config
	// uses (0o600). Plain os.Create would inherit umask and leave the
	// secrets in the backup readable by other local users.
	outputFile, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't backup config file."}
	}
	defer outputFile.Close() //nolint:errcheck
	_, err = io.Copy(outputFile, inputFile)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't write config file."}
	}
	// The copy was successful, so now delete the original file
	if err := inputFile.Close(); err != nil {
		return modsError{Err: err, ReasonText: "Couldn't close config file."}
	}
	if err := outputFile.Close(); err != nil {
		return modsError{Err: err, ReasonText: "Couldn't close backup config file."}
	}
	err = os.Remove(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't remove config file."}
	}
	err = WriteDefaultFile(config.SettingsPath)
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't write new config file."}
	}
	fmt.Fprintln(os.Stderr, "\nSettings restored to defaults!")
	fmt.Fprintf(os.Stderr,
		"\n  %s %s\n\n",
		StderrStyles().Comment.Render("Your old settings have been saved to:"),
		StderrStyles().Link.Render(backupPath),
	)
	return nil
}

// nextBackupPath returns the first path among base, base.1, base.2, ...
// that does not already exist, so resetSettings never silently overwrites
// a previous backup. The loop bound prevents an unbounded retry storm if
// the filesystem somehow returns errors that look like ErrNotExist for
// every candidate.
func nextBackupPath(base string) (string, error) {
	const maxAttempts = 1000
	candidate := base
	for i := 0; i < maxAttempts; i++ {
		if _, err := os.Stat(candidate); errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		candidate = fmt.Sprintf("%s.%d", base, i+1)
	}
	return "", fmt.Errorf("could not find an unused backup name starting at %q", base)
}

func removeLegacySessionFile(id string) error {
	path := filepath.Join(filepath.Dir(config.SessionDir), "conversations", id+".gob")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func listSessions(raw bool) error {
	sessions, err := db.List()
	if err != nil {
		return modsError{Err: err, ReasonText: "Couldn't list saves."}
	}

	if len(sessions) == 0 {
		fmt.Fprintln(os.Stderr, "No sessions found.")
		return nil
	}

	if IsInputTTY() && IsOutputTTY() && !raw {
		return runSessionBrowser(sessions)
	}
	printList(sessions)
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
		_, _ = lipgloss.Fprintln(os.Stdout, s)
	}
}

func printList(sessions []Session) {
	for _, session := range sessions {
		_, _ = lipgloss.Fprintf(
			os.Stdout,
			"%s\t%s\t%s\n",
			StdoutStyles().ShaHash.Render(session.ID[:ShortIDLength]),
			session.Title,
			StdoutStyles().Timeago.Render(timeago.Of(session.UpdatedAt)),
		)
	}
}

func saveSession(mods *Mods) error {
	if config.NoSave {
		_, _ = lipgloss.Fprintf(
			os.Stderr,
			"\nSession was not saved because %s is enabled.\n",
			StderrStyles().InlineCode.Render("--no-save"),
		)
		return nil
	}

	id, title, err := persistSession(mods)
	if err != nil {
		return err
	}

	_, _ = lipgloss.Fprintln(
		os.Stderr,
		"\nSession saved:",
		StderrStyles().InlineCode.Render(id[:ShortIDLength]),
		StderrStyles().Comment.Render(title),
	)
	return nil
}

func persistSession(mods *Mods) (string, string, error) {
	// if message is a sha1, use the last prompt instead.
	id := config.SessionWriteToID
	title := strings.TrimSpace(config.SessionWriteToTitle)

	if IDPattern.MatchString(title) || title == "" {
		title = FirstLine(lastPrompt(mods.Messages()))
	}

	errReason := fmt.Sprintf(
		"There was a problem saving session %s. Use %s to disable persistence.",
		config.SessionWriteToID,
		StderrStyles().InlineCode.Render("--no-save"),
	)
	if err := db.SaveSession(
		id,
		title,
		config.API,
		config.Model,
		mods.Messages(),
		mods.ApprovalRules(),
	); err != nil {
		return "", "", modsError{Err: err, ReasonText: errReason}
	}
	return id, title, nil
}

// isNoArgs reports whether the invocation is effectively empty (no prompt and
// no side-effect action). It deliberately checks Config fields directly rather
// than scanning sessionActionFlags: it must also consider ListRoles, Dirs and
// ShowHelp, which are NOT part of the mutually-exclusive action set. Keep this
// in sync with sessionActionFlags when adding a new session action.
func isNoArgs() bool {
	return config.Prefix == "" &&
		!config.ShowHelp &&
		!config.List &&
		!config.Chat &&
		!config.ListRoles &&
		!config.ListPrompts &&
		!config.ListSkills &&
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

	if !config.AskModel && foundModel {
		return nil
	}

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
		),
	).
		WithTheme(themeFrom(config.Theme)).
		Run()
}

//nolint:mnd
func isCompletionCmd(args []string) bool {
	if len(args) <= 1 {
		return false
	}
	if args[1] == "__complete" || args[1] == "__completeNoDesc" {
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
		if arg == "--version" || arg == "-v" || arg == "--help" || arg == "-h" {
			return true
		}
	}
	return false
}

func themeFrom(theme string) huh.Theme {
	switch strings.ToLower(strings.TrimSpace(theme)) {
	case "dracula":
		return huh.ThemeFunc(huh.ThemeDracula)
	case "catppuccin":
		return huh.ThemeFunc(huh.ThemeCatppuccin)
	case "base16":
		return huh.ThemeFunc(huh.ThemeBase16)
	default:
		return huh.ThemeFunc(huh.ThemeCharm)
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
