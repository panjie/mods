// Package cli flag registration helpers and shared flag-name constants.
package cli

import "github.com/spf13/pflag"

const (
	flagTierAnnotation     = "mods/tier"
	flagCategoryAnnotation = "mods/category"
	flagTierAdvanced       = "advanced"
)

const (
	flagCategoryModelAPI    = "Model & API"
	flagCategorySession     = "Session"
	flagCategoryInputOutput = "Input & Output"
	flagCategoryConfigUI    = "Configuration & UI"
	flagCategoryRoles       = "Roles"
	flagCategoryWebSearch   = "Web Search"
	flagCategoryToolsReview = "Tools, Review & Reasoning"
	flagCategoryMCP         = "MCP"
	flagCategoryModelParams = "Model Parameters"
	flagCategoryDebug       = "Debug"
	flagCategoryOther       = "Other"
)

var flagCategoryOrder = []string{
	flagCategoryModelAPI,
	flagCategorySession,
	flagCategoryInputOutput,
	flagCategoryConfigUI,
	flagCategoryRoles,
	flagCategoryWebSearch,
	flagCategoryToolsReview,
	flagCategoryMCP,
	flagCategoryModelParams,
	flagCategoryDebug,
	flagCategoryOther,
}

// Names of session-action flags. These are the flags that select a single
// side-effect (open settings, browse conversations, MCP listing, reset
// settings) instead of starting a chat. They are mutually exclusive with
// each other and several of them share completion/suggestion logic, so the
// canonical lists live here to avoid the four-way duplication that previously
// existed between initFlags, MarkFlagsMutuallyExclusive, isNoArgs and the
// conversation browser.
const (
	flagSettings      = "settings"
	flagListSessions  = "list-sessions"
	flagChat          = "chat"
	flagContinue      = "continue"
	flagContinueLast  = "continue-last"
	flagResetSettings = "reset-settings"
	flagConfig        = "config"
	flagMCPList       = "mcp-list"
	flagMCPListTools  = "mcp-list-tools"
	flagListPrompts   = "list-prompts"
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
	flagMCPList,
	flagMCPListTools,
	flagListPrompts,
}

// conversationCompleteFlags take a conversation id or title as their value and
// therefore participate in shell completion.
var conversationCompleteFlags = []string{
	flagContinue,
}

// flagDesc renders the help text for a flag from the shared Help map.
func flagDesc(name string) string {
	return StdoutStyles().FlagDesc.Render(Help[name])
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

func flagVisibleInUsage(f *pflag.Flag, showAll bool) bool {
	if f.Hidden {
		return false
	}
	if showAll {
		return true
	}
	return len(f.Annotations[flagTierAnnotation]) == 0 ||
		f.Annotations[flagTierAnnotation][0] != flagTierAdvanced
}

func flagCategory(f *pflag.Flag) string {
	values := f.Annotations[flagCategoryAnnotation]
	if len(values) == 0 || values[0] == "" {
		return flagCategoryOther
	}
	return values[0]
}

func groupedUsageFlags(flags *pflag.FlagSet, showAll bool) map[string][]*pflag.Flag {
	groups := make(map[string][]*pflag.Flag)
	flags.VisitAll(func(f *pflag.Flag) {
		if !flagVisibleInUsage(f, showAll) {
			return
		}
		category := flagCategory(f)
		groups[category] = append(groups[category], f)
	})
	return groups
}
