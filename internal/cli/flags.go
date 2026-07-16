// Package cli flag registration helpers and shared flag-name constants.
package cli

import "github.com/spf13/pflag"

const (
	flagTierAnnotation     = "mods/tier"
	flagCategoryAnnotation = "mods/category"
	flagTierAdvanced       = "advanced"
)

const (
	flagCategoryModelProvider     = "Model & Provider"
	flagCategoryModesSessions     = "Modes & Sessions"
	flagCategoryPromptContext     = "Prompt & Context"
	flagCategoryWorkspaceReview   = "Workspace & Review"
	flagCategoryToolsIntegrations = "Tools & Integrations"
	flagCategoryOutputDisplay     = "Output & Display"
	flagCategoryConfigMaintenance = "Configuration & Maintenance"
	flagCategoryHelpDiagnostics   = "Help & Diagnostics"
	flagCategoryOther             = "Other"
)

type flagCategorySpec struct {
	Name  string
	Flags []string
}

// flagCategorySpecs is the single source of truth for both category order and
// flag order in --help. Keep every public flag here; groupedUsageFlags retains
// an Other fallback so a newly added flag is still visible until categorized.
var flagCategorySpecs = []flagCategorySpec{
	{
		Name: flagCategoryModelProvider,
		Flags: []string{
			"api", "model", "ask-model", "max-tokens", "no-limit",
			"max-retries", "http-proxy",
		},
	},
	{
		Name: flagCategoryModesSessions,
		Flags: []string{
			flagChat, "plan", "think", flagContinue, flagContinueLast,
			flagListSessions, "no-save",
		},
	},
	{
		Name: flagCategoryPromptContext,
		Flags: []string{
			"editor", "role", "list-roles", "image", "stdin-image",
			"clipboard-image", "no-instructions", flagListPrompts,
		},
	},
	{
		Name:  flagCategoryWorkspaceReview,
		Flags: []string{"workspace", "review-mode"},
	},
	{
		Name: flagCategoryToolsIntegrations,
		Flags: []string{
			"max-tool-rounds", flagListTools, "skills-dirs", flagListSkills,
			"web-search", flagListMCPs,
		},
	},
	{
		Name: flagCategoryOutputDisplay,
		Flags: []string{
			"format", "minimal", "raw", "word-wrap", "hide-tool-status",
			"show-token-usage",
		},
	},
	{
		Name: flagCategoryConfigMaintenance,
		Flags: []string{
			flagConfig, flagSettings, "dirs", flagResetSettings,
		},
	},
	{
		Name:  flagCategoryHelpDiagnostics,
		Flags: []string{"help", "version", "debug"},
	},
}

// Names of session-action flags. These are the flags that select a single
// side-effect (open settings, browse sessions, MCP listing, reset
// settings) instead of starting a chat. They are mutually exclusive with
// each other and several of them share completion/suggestion logic, so the
// canonical lists live here to avoid the four-way duplication that previously
// existed between initFlags, MarkFlagsMutuallyExclusive, isNoArgs and the
// session browser.
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
)

// sessionActionFlags are mutually exclusive: at most one may be passed per
// invocation. MarkFlagsMutuallyExclusive consumes this slice verbatim.
var sessionActionFlags = []string{
	flagSettings,
	flagListSessions,
	flagContinue,
	flagContinueLast,
	flagResetSettings,
	flagConfig,
	flagListMCPs,
	flagListTools,
	flagListPrompts,
	flagListSkills,
}

// sessionCompleteFlags take a session id or title as their value and
// therefore participate in shell completion.
var sessionCompleteFlags = []string{
	flagContinue,
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

// regInt64 registers an int64 flag with auto-rendered help.
func regInt64(flags *pflag.FlagSet, p *int64, name string, def int64) {
	flags.Int64Var(p, name, def, flagDesc(name))
}

// regFloat64 registers a float64 flag with auto-rendered help.
func regFloat64(flags *pflag.FlagSet, p *float64, name string, def float64) {
	flags.Float64Var(p, name, def, flagDesc(name))
}

// regStrArr registers a []string flag with auto-rendered help, optional shorthand.
func regStrArr(flags *pflag.FlagSet, p *[]string, name, short string, def []string) {
	if short != "" {
		flags.StringArrayVarP(p, name, short, def, flagDesc(name))
		return
	}
	flags.StringArrayVar(p, name, def, flagDesc(name))
}

func markAdvanced(flags *pflag.FlagSet, names ...string) {
	for _, name := range names {
		flag := flags.Lookup(name)
		if flag == nil {
			continue
		}
		if flag.Annotations == nil {
			flag.Annotations = map[string][]string{}
		}
		flag.Annotations[flagTierAnnotation] = []string{flagTierAdvanced}
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
		markCategory(flags, category.Name, category.Flags...)
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
		for _, name := range category.Flags {
			f := flags.Lookup(name)
			if !flagVisibleInUsage(f) {
				continue
			}
			groups[category.Name] = append(groups[category.Name], f)
			seen[name] = struct{}{}
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
