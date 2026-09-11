// Package cli flag registration helpers and shared flag-name constants.
package cli

import (
	"strings"

	"github.com/spf13/pflag"
)

const (
	flagTierAnnotation     = "mods/tier"
	flagCategoryAnnotation = "mods/category"
	flagTierAdvanced       = "advanced"
)

const (
	flagCategoryModelProvider     = "Model & Provider"
	flagCategoryModesSessions     = "Modes & Sessions"
	flagCategoryPromptContext     = "Prompt & Context"
	flagCategoryReview            = "Review"
	flagCategoryToolsIntegrations = "Tools & Integrations"
	flagCategoryOutputDisplay     = "Output & Display"
	flagCategoryConfigMaintenance = "Configuration & Maintenance"
	flagCategoryHelpDiagnostics   = "Help & Diagnostics"
	flagCategoryOther             = "Other"
)

// flagRole classifies how a flag participates in the one-shot invocation
// predicates (isNoArgs, hasChatSessionAction, and the first-run auto-config
// skips). The bits are orthogonal, so a new flag needs exactly one table entry
// instead of an edit in every predicate.
type flagRole uint8

const (
	// roleExclusive marks a flag that selects one side effect and is mutually
	// exclusive with the other such flags.
	roleExclusive flagRole = 1 << iota
	// roleSessionComplete marks a flag whose value is a session id or title and
	// therefore participates in shell completion.
	roleSessionComplete
	// roleNoArgs marks a flag whose presence means the invocation is not empty,
	// so mods must not fall through to the "no prompt, no action" path.
	roleNoArgs
	// roleBlocksChat marks a flag that cannot be combined with --chat.
	roleBlocksChat
	// roleBlocksAutoConfig marks a flag that skips first-run auto configuration.
	roleBlocksAutoConfig
	// roleBlocksPassiveAutoConfig marks a flag that skips the cleanup of a
	// config file that auto configuration created.
	roleBlocksPassiveAutoConfig
)

// flagSpec declares one public flag's metadata: its usage category and order,
// its tier, and its role in the one-shot predicates. Registration itself (type,
// default, bound field) stays in initFlags because it is typed code; the guard
// test asserts table and registration agree in both directions, so a flag
// cannot be half-added.
type flagSpec struct {
	Name  string
	Short string
	// Advanced marks the flag as an advanced-tier entry in --help and in the
	// runtime self-help catalog.
	Advanced bool
	// Set reports whether this flag currently selects its side effect. It reads
	// the bound config field at call time, so a value supplied by mods.yml or
	// the environment counts exactly as an explicit flag does. Only flags that
	// carry one of the predicate roles need it.
	Set  func() bool
	Role flagRole
}

type flagCategorySpec struct {
	Name  string
	Flags []flagSpec
}

// flagCategorySpecs is the single source of truth for a public flag's category,
// order in --help, tier and one-shot role. Keep every public flag here;
// groupedUsageFlags retains an Other fallback so a newly added flag is still
// visible until categorized, and TestFlagTableMatchesRegistration fails when
// the table and the registrations disagree.
var flagCategorySpecs = []flagCategorySpec{
	{
		Name: flagCategoryModelProvider,
		Flags: []flagSpec{
			{Name: "api", Short: "a"},
			{Name: "model", Short: "m"},
			{Name: "max-retries", Advanced: true},
			{Name: "http-proxy", Short: "x", Advanced: true},
		},
	},
	{
		Name: flagCategoryModesSessions,
		Flags: []flagSpec{
			{Name: flagChat, Set: func() bool { return config.Chat }, Role: roleNoArgs},
			{Name: "think", Short: "t"},
			{Name: flagContinue, Short: "C", Role: roleExclusive | roleSessionComplete},
			{Name: flagContinueLast, Short: "c", Role: roleExclusive},
			{Name: flagListSessions, Short: "l", Set: func() bool { return config.List }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
			{Name: "no-save", Short: "n", Advanced: true},
		},
	},
	{
		Name: flagCategoryPromptContext,
		Flags: []flagSpec{
			{Name: "editor", Short: "e"},
			{Name: "role", Short: "r"},
			{Name: "list-roles", Set: func() bool { return config.ListRoles }, Role: roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
			{Name: "image", Short: "i"},
			{Name: "stdin-image", Advanced: true},
			{Name: "clipboard-image", Short: "I", Advanced: true},
			{Name: "no-instructions", Advanced: true},
			{Name: flagListPrompts, Set: func() bool { return config.ListPrompts }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
		},
	},
	{
		Name: flagCategoryReview,
		Flags: []flagSpec{
			{Name: "review-mode", Short: "V"},
			{Name: "no-review", Short: "N"},
		},
	},
	{
		Name: flagCategoryToolsIntegrations,
		Flags: []flagSpec{
			{Name: flagListTools, Advanced: true, Set: func() bool { return config.MCPListTools }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
			{Name: "skills-dirs"},
			{Name: flagListSkills, Set: func() bool { return config.ListSkills }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
			{Name: "web-search"},
			{Name: flagListMCPs, Advanced: true, Set: func() bool { return config.MCPList }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
		},
	},
	{
		Name: flagCategoryOutputDisplay,
		Flags: []flagSpec{
			{Name: "format", Short: "f"},
			{Name: "minimal"},
			{Name: "raw"},
			{Name: "word-wrap", Advanced: true},
			{Name: "hide-tool-status", Advanced: true},
			{Name: "show-token-usage", Short: "s", Advanced: true},
		},
	},
	{
		Name: flagCategoryConfigMaintenance,
		Flags: []flagSpec{
			{Name: flagConfig, Set: func() bool { return config.ConfigSetup }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig},
			{Name: flagSettings, Set: func() bool { return config.Settings }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig},
			{Name: "dirs", Set: func() bool { return config.Dirs }, Role: roleNoArgs | roleBlocksChat | roleBlocksAutoConfig | roleBlocksPassiveAutoConfig},
			{Name: flagResetSettings, Set: func() bool { return config.ResetSettings }, Role: roleExclusive | roleNoArgs | roleBlocksChat | roleBlocksAutoConfig},
		},
	},
	{
		Name: flagCategoryHelpDiagnostics,
		Flags: []flagSpec{
			{Name: "help", Short: "h", Set: func() bool { return config.ShowHelp }, Role: roleNoArgs},
			{Name: "version", Short: "v"},
			{Name: "debug", Short: "D", Advanced: true},
		},
	},
}

// Names of session-action flags. These are the flags that select a single
// side-effect (open settings, browse sessions, MCP listing, reset
// settings) instead of starting a chat. They are mutually exclusive with
// each other and several of them share completion/suggestion logic, so the
// canonical lists live in the flag table above.
const (
	flagSettings      = "settings"
	flagListSessions  = "list-sessions"
	flagChat          = "chat"
	flagContinue      = "continue"
	flagContinueLast  = "continue-last"
	flagResetSettings = "reset-settings"
	flagConfig        = "config"
	flagListMCPs      = "list-mcps"
	flagListTools     = "list-tools"
	flagListPrompts   = "list-prompts"
	flagListSkills    = "list-skills"

	settingsEditorFlagValue = "__MODS_OPEN_SETTINGS_EDITOR__"
)

var (
	// sessionActionFlags are mutually exclusive: at most one may be passed per
	// invocation. MarkFlagsMutuallyExclusive consumes this slice verbatim.
	sessionActionFlags = flagNamesWithRole(roleExclusive)
	// sessionCompleteFlags take a session id or title as their value and
	// therefore participate in shell completion.
	sessionCompleteFlags = flagNamesWithRole(roleSessionComplete)
)

// flagNamesWithRole returns the names of the flags carrying role, in usage
// order.
func flagNamesWithRole(role flagRole) []string {
	var names []string
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			if spec.Role&role != 0 {
				names = append(names, spec.Name)
			}
		}
	}
	return names
}

// anyRoleSelected reports whether any flag carrying role currently selects its
// side effect. Set reads the bound config value, so this matches the previous
// hand-written predicates exactly, including values that came from config.
func anyRoleSelected(role flagRole) bool {
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			if spec.Role&role != 0 && spec.Set != nil && spec.Set() {
				return true
			}
		}
	}
	return false
}

// flagDesc renders the help text for a flag from the shared Help map.
func flagDesc(name string) string {
	// Keep registration side-effect free. The usage renderer applies styles
	// when help is actually requested; styling here would initialize
	// StdoutStyles for every command and trigger an unnecessary terminal
	// background query during startup.
	return Help[name]
}

// regStr registers a string flag with auto-rendered help, optional shorthand.
func regStr(flags *pflag.FlagSet, p *string, name, short, def string) {
	if short != "" {
		flags.StringVarP(p, name, short, def, flagDesc(name))
		return
	}
	flags.StringVar(p, name, def, flagDesc(name))
}

// regBool registers a bool flag with auto-rendered help, optional shorthand.
func regBool(flags *pflag.FlagSet, p *bool, name, short string, def bool) {
	if short != "" {
		flags.BoolVarP(p, name, short, def, flagDesc(name))
		return
	}
	flags.BoolVar(p, name, def, flagDesc(name))
}

// regInt registers an int flag with auto-rendered help.
func regInt(flags *pflag.FlagSet, p *int, name string, def int) {
	flags.IntVar(p, name, def, flagDesc(name))
}

type settingsFlagValue struct {
	settings   *bool
	importYAML *bool
	yaml       *string
}

func newSettingsFlagValue(settings, importYAML *bool, yamlInput *string) *settingsFlagValue {
	return &settingsFlagValue{
		settings:   settings,
		importYAML: importYAML,
		yaml:       yamlInput,
	}
}

func (v *settingsFlagValue) Set(value string) error {
	*v.settings = true
	*v.importYAML = value != settingsEditorFlagValue
	if *v.importYAML {
		*v.yaml = value
	} else {
		*v.yaml = ""
	}
	return nil
}

func (v *settingsFlagValue) String() string {
	if v == nil || v.yaml == nil {
		return ""
	}
	return *v.yaml
}

func (*settingsFlagValue) Type() string {
	return "yaml"
}

func regSettingsFlag(flags *pflag.FlagSet, c *Config) {
	flag := flags.VarPF(
		newSettingsFlagValue(&c.Settings, &c.SettingsImport, &c.SettingsYAML),
		flagSettings,
		"",
		flagDesc(flagSettings),
	)
	flag.NoOptDefVal = settingsEditorFlagValue
}

// normalizeSettingsArgs lets --settings retain its historical valueless form
// while also accepting a space-separated YAML argument. pflag stops consuming
// the next argument whenever NoOptDefVal is set, so rewrite only that spelling
// to the equivalent --settings=<yaml> form before Cobra parses it.
func normalizeSettingsArgs(args []string) []string {
	normalized := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			normalized = append(normalized, args[i:]...)
			break
		}
		if arg == "--"+flagSettings &&
			i+1 < len(args) &&
			!isKnownCLIFlag(args[i+1]) {
			normalized = append(normalized, arg+"="+args[i+1])
			i++
			continue
		}
		normalized = append(normalized, arg)
	}
	return normalized
}

func hasSettingsArg(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if arg == "--"+flagSettings || strings.HasPrefix(arg, "--"+flagSettings+"=") {
			return true
		}
	}
	return false
}

func isKnownCLIFlag(arg string) bool {
	if arg == "--" {
		return true
	}
	if strings.HasPrefix(arg, "--") {
		name := strings.TrimPrefix(arg, "--")
		if index := strings.IndexByte(name, '='); index >= 0 {
			name = name[:index]
		}
		return rootCmd.Flags().Lookup(name) != nil
	}
	if strings.HasPrefix(arg, "-") && len(arg) == 2 {
		return rootCmd.Flags().ShorthandLookup(arg[1:]) != nil
	}
	return false
}

// regStrArr registers a []string flag with auto-rendered help, optional shorthand.
func regStrArr(flags *pflag.FlagSet, p *[]string, name, short string, def []string) {
	if short != "" {
		flags.StringArrayVarP(p, name, short, def, flagDesc(name))
		return
	}
	flags.StringArrayVar(p, name, def, flagDesc(name))
}

// extractSkillsDirsAction keeps the existing "--skills-dirs DIR" spelling
// while allowing a genuinely valueless "--skills-dirs" to act as a read-only
// listing command. pflag's NoOptDefVal cannot be used here because it would
// stop consuming the space-separated DIR form and turn DIR into prompt input.
func extractSkillsDirsAction(args []string) ([]string, bool) {
	out := make([]string, 0, len(args))
	show := false
	for i := 0; i < len(args); i++ {
		if args[i] != "--skills-dirs" {
			out = append(out, args[i])
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			out = append(out, args[i], args[i+1])
			i++
			continue
		}
		show = true
	}
	return out, show
}

// applyFlagTiers marks the advanced tier from the flag table, so the tier is
// declared once per flag rather than repeated in an initFlags name list.
func applyFlagTiers(flags *pflag.FlagSet) {
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			if !spec.Advanced {
				continue
			}
			flag := flags.Lookup(spec.Name)
			if flag == nil {
				continue
			}
			if flag.Annotations == nil {
				flag.Annotations = map[string][]string{}
			}
			flag.Annotations[flagTierAnnotation] = []string{flagTierAdvanced}
		}
	}
}

func markCategory(flags *pflag.FlagSet, category string, names ...string) {
	for _, name := range names {
		flag := flags.Lookup(name)
		if flag == nil {
			continue
		}
		if flag.Annotations == nil {
			flag.Annotations = map[string][]string{}
		}
		flag.Annotations[flagCategoryAnnotation] = []string{category}
	}
}

func applyFlagCategories(flags *pflag.FlagSet) {
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			markCategory(flags, category.Name, spec.Name)
		}
	}
}

func flagVisibleInUsage(f *pflag.Flag) bool {
	return f != nil && !f.Hidden
}

func flagIsAdvanced(f *pflag.Flag) bool {
	values := f.Annotations[flagTierAnnotation]
	return len(values) > 0 && values[0] == flagTierAdvanced
}

func flagCategory(f *pflag.Flag) string {
	values := f.Annotations[flagCategoryAnnotation]
	if len(values) == 0 || values[0] == "" {
		return flagCategoryOther
	}
	return values[0]
}

func groupedUsageFlags(flags *pflag.FlagSet) map[string][]*pflag.Flag {
	groups := make(map[string][]*pflag.Flag)
	seen := make(map[string]struct{})
	for _, category := range flagCategorySpecs {
		for _, spec := range category.Flags {
			f := flags.Lookup(spec.Name)
			if !flagVisibleInUsage(f) {
				continue
			}
			groups[category.Name] = append(groups[category.Name], f)
			seen[spec.Name] = struct{}{}
		}
	}

	flags.VisitAll(func(f *pflag.Flag) {
		if !flagVisibleInUsage(f) {
			return
		}
		if _, ok := seen[f.Name]; ok {
			return
		}
		groups[flagCategoryOther] = append(groups[flagCategoryOther], f)
	})
	return groups
}
