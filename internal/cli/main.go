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
	"charm.land/lipgloss/v2"
	timeago "github.com/caarlos0/timea.go"
	"github.com/charmbracelet/x/editor"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
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
	showSkillsDirs    bool

	rootCmd = &cobra.Command{
		Use:           "mods",
		Short:         helpIntroSummary,
		SilenceUsage:  true,
		SilenceErrors: true,
		Example:       randomExample(),
		RunE: func(cmd *cobra.Command, args []string) error {
			debug.SetEnabled(config.Debug)
			debugStartup()
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
	return execute()
}

// registerFlags declares every public flag on flags, binding parsed values
// into c (and memprofileFlag). It is deliberately separate from initFlags so
// the pre-Cobra help/version probe can reuse these exact definitions against a
// throwaway FlagSet and a scratch Config: parsing argv with pflag itself is the
// only way to stay in agreement with the parsing Cobra will perform.
func registerFlags(flags *pflag.FlagSet, c *Config, memprofileFlag *bool) {
	regStr(flags, &c.Model, "model", "m", c.Model)
	regStr(flags, &c.API, "api", "a", c.API)
	regStr(flags, &c.HTTPProxy, "http-proxy", "x", c.HTTPProxy)
	fF := flags.VarPF(newFormatFlag(c.Format, &c.Format), "format", "f", flagDesc("format"))
	fF.NoOptDefVal = "markdown"
	regBool(flags, &c.Minimal, "minimal", "", c.Minimal)
	regBool(flags, &c.Raw, "raw", "", c.Raw)
	regStr(flags, &c.Continue, "continue", "C", "")
	regBool(flags, &c.ContinueLast, "continue-last", "c", false)
	regBool(flags, &c.List, flagListSessions, "l", c.List)
	regBool(flags, &c.Chat, flagChat, "", false)
	regBool(flags, &c.HideToolStatus, "hide-tool-status", "", c.HideToolStatus)
	regBool(flags, &c.ShowTokenUsage, "show-token-usage", "s", c.ShowTokenUsage)
	regBool(flags, &c.ShowHelp, "help", "h", false)
	regBool(flags, &c.Version, "version", "v", false)
	regInt(flags, &c.MaxRetries, "max-retries", c.MaxRetries)
	regInt(flags, &c.WordWrap, "word-wrap", c.WordWrap)
	regStrArr(flags, &c.SkillsDirs, "skills-dirs", "", c.SkillsDirs)
	regBool(flags, &c.NoSave, "no-save", "n", c.NoSave)
	regBool(flags, &c.NoInstructions, "no-instructions", "", c.NoInstructions)
	regBool(flags, &c.ResetSettings, "reset-settings", "", c.ResetSettings)
	regSettingsFlag(flags, c)
	regBool(flags, &c.ConfigSetup, "config", "", false)
	regBool(flags, &c.Dirs, "dirs", "", false)
	regStr(flags, &c.Role, "role", "r", c.Role)
	regBool(flags, &c.ListRoles, "list-roles", "", c.ListRoles)
	regBool(flags, &c.ListPrompts, flagListPrompts, "", c.ListPrompts)
	regBool(flags, &c.ListSkills, flagListSkills, "", c.ListSkills)
	regBool(flags, &c.OpenEditor, "editor", "e", false)
	regBool(flags, &c.MCPList, "list-mcps", "", false)
	regBool(flags, &c.MCPListTools, "list-tools", "", false)

	regBool(flags, &c.WebSearch, "web-search", "", c.WebSearch)
	regStrArr(flags, &c.Images, "image", "i", c.Images)
	regBool(flags, &c.StdinImage, "stdin-image", "", c.StdinImage)
	regBool(flags, &c.ClipboardImage, "clipboard-image", "I", c.ClipboardImage)
	regBool(flags, &c.Debug, "debug", "D", c.Debug)
	regBool(flags, &c.Think, "think", "t", c.Think)
	flags.VarP(newReviewFlag(c.ReviewMode, &c.ReviewMode), "review-mode", "V", flagDesc("review-mode"))
	noReviewFlag := flags.VarPF(newReviewNeverFlag(&c.ReviewMode), "no-review", "N", flagDesc("no-review"))
	noReviewFlag.NoOptDefVal = "true"

	flags.BoolVar(memprofileFlag, "memprofile", false, "Write memory profiles to CWD")
	_ = flags.MarkHidden("memprofile")
}

func initFlags() {
	flags := rootCmd.Flags()
	registerFlags(flags, &config, &memprofile)

	applyFlagTiers(flags)
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

func initFlagsOnce() {
	if rootCmd.Flags().Lookup("model") != nil {
		return
	}
	initFlags()
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

func execute() (exitCode int) {
	defer func() {
		if err := maybeWriteMemProfile(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			exitCode = 1
		}
	}()
	// Cobra itself decides whether this invocation is a help/version request;
	// this probe only decides whether the expensive pre-Cobra setup (config
	// file load, session DB open, migrations) can be skipped. It must agree with
	// Cobra: when the two disagree the prompt path runs against a configuration
	// that was never loaded.
	helpOrVersion := helpOrVersionRequested(os.Args)
	if helpOrVersion {
		initFlagsOnce()
		rootCmd.SetArgs(os.Args[1:])
		if err := rootCmd.Execute(); err != nil {
			handleError(err)
			return 1
		}
		return 0
	}
	var err error
	config, err = Ensure()
	if err != nil {
		handleError(modsError{Err: err, ReasonText: "Could not load your configuration file."})
		if !hasSettingsArg(os.Args) && !slices.Contains(os.Args, "--config") {
			return 1
		}
	}

	// XXX: this must come after creating the config.
	initFlags()

	if !isCompletionCmd(os.Args) {
		if err := MigrateDefaultStorage(config.SessionDir); err != nil {
			handleError(modsError{Err: err, ReasonText: "Could not migrate session storage."})
			return 1
		}
		db, err = Open(filepath.Join(config.SessionDir, "mods.db"))
		if err != nil {
			handleError(modsError{Err: err, ReasonText: "Could not open database."})
			return 1
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

	args := normalizeSettingsArgs(os.Args[1:])
	args, showDirs := extractSkillsDirsAction(args)
	showSkillsDirs = showDirs
	rootCmd.SetArgs(args)

	if err := rootCmd.Execute(); err != nil {
		handleError(err)
		return 1
	}
	return 0
}

func debugStartup() {
	if !debug.Enabled() {
		return
	}
	fields := []DebugField{
		{Label: "config", Value: config.SettingsPath},
		{Label: "provider", Value: config.API + "/" + config.Model},
		{Label: "output", Value: fmt.Sprintf("role=%s · format=%s · raw=%v", config.Role, config.Format, config.Raw)},
		{Label: "sessions", Value: config.SessionDir},
	}
	if config.PortableDir != "" {
		fields = append(fields, DebugField{Label: "portable", Value: config.PortableDir})
	}
	debug.Print(DebugSection{Title: "startup", Fields: fields})
}

func maybeWriteMemProfile() error {
	if !memprofile {
		return nil
	}

	heap, err := os.Create("mods_heap.profile")
	if err != nil {
		return fmt.Errorf("create heap profile: %w", err)
	}
	defer func() { _ = heap.Close() }()
	allocs, err := os.Create("mods_allocs.profile")
	if err != nil {
		return fmt.Errorf("create allocations profile: %w", err)
	}
	defer func() { _ = allocs.Close() }()

	if err := pprof.Lookup("heap").WriteTo(heap, 0); err != nil {
		return fmt.Errorf("write heap profile: %w", err)
	}
	if err := pprof.Lookup("allocs").WriteTo(allocs, 0); err != nil {
		return fmt.Errorf("write allocations profile: %w", err)
	}
	return nil
}

func handleError(err error) {
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

		// Skip the error details if the user simply canceled an interactive prompt.
		// Render only the inner err message so the ReasonText (already
		// shown in the header above) is not repeated by Error.Error().
		if merr.Err != nil && !errors.Is(merr.Err, errSetupCanceled) {
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
		return runSessionBrowser(sessions, config.NerdFontGlyphs)
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
// no side-effect action). The flags that make an invocation non-empty declare
// roleNoArgs in flagCategorySpecs; that set is wider than the mutually
// exclusive session actions, because ShowHelp, Chat, ListRoles and Dirs also
// select behaviour without being part of it.
func isNoArgs() bool {
	return config.Prefix == "" && !showSkillsDirs && !anyRoleSelected(roleNoArgs)
}

func askInfo() error {
	apis, opts, foundModel := askInfoOptions(&config)

	if config.ContinueLast {
		found, err := db.FindHEAD()
		if err == nil && found != nil && found.Model != nil && found.API != nil {
			config.Model = *found.Model
			config.API = *found.API
			foundModel = true
		}
	}

	if foundModel {
		return nil
	}
	if len(apis) == 0 {
		return fmt.Errorf("no API models are configured; run %s to add one", "mods --config")
	}

	return runSetupPicker(apis, opts)
}

func askInfoOptions(cfg *Config) ([]setupOption, map[string][]setupOption, bool) {
	var foundModel bool
	apis := make([]setupOption, 0, len(cfg.APIs))
	opts := map[string][]setupOption{}
	for _, api := range cfg.APIs {
		if len(api.Models) == 0 {
			continue
		}
		apis = append(apis, newSetupOption(api.Name, api.Name))
		for name, model := range api.Models {
			opts[api.Name] = append(opts[api.Name], newSetupOption(name, name))

			// Checks whether this is the configured model and normalizes aliases
			// so later lookups can use the canonical API and model names.
			if (cfg.API == "" || cfg.API == api.Name) &&
				(cfg.Model == name || slices.Contains(model.Aliases, cfg.Model)) {
				// if it is, adjusts api and model so its cheaper later on.
				cfg.API = api.Name
				cfg.Model = name
				foundModel = true
			}
		}
	}
	return apis, opts, foundModel
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

// helpOrVersionRequested reports whether Cobra will handle this invocation as a
// --help or --version request instead of running a prompt. osArgs is the full
// argument vector, program name included (the same convention isCompletionCmd
// uses).
//
// The flags are parsed with pflag against the real registrations bound to a
// scratch Config, mirroring the checks Cobra performs after it parses the real
// flag set. A string scan cannot stand in for this: it would not honor the "--"
// terminator, `--help=false`, or value-taking flags such as `--model --help`,
// and any disagreement makes the prompt path skip Ensure() and run with an
// unloaded configuration and no session database.
func helpOrVersionRequested(osArgs []string) bool {
	flags := pflag.NewFlagSet("mods", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var scratch Config
	var scratchMemprofile bool
	registerFlags(flags, &scratch, &scratchMemprofile)
	if err := flags.Parse(osArgs[1:]); err != nil {
		// Unknown or malformed flags are Cobra's to report on the real flag set.
		return false
	}
	// Mirror cobra.Command.execute: help first, then version, and only when a
	// version template is worth printing. The version check reads the package
	// Version rather than rootCmd.Version: this function is reachable from the
	// rootCmd initializer (through RunE -> first-run predicates), and referring
	// to rootCmd here would make Go report an initialization cycle for rootCmd.
	// buildVersion always assigns rootCmd.Version from Version during init, so
	// the two agree by the time anything can call this.
	if help, _ := flags.GetBool("help"); help {
		return true
	}
	if Version == "" {
		return false
	}
	version, _ := flags.GetBool("version")
	return version
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
