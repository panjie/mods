package cli

import (
	"strings"

	"github.com/panjie/mods/internal/selfhelp"
	"github.com/spf13/pflag"
)

var registeredSelfHelpFlags []selfhelp.FlagGroup

func selfHelpFlagGroups(flags *pflag.FlagSet) []selfhelp.FlagGroup {
	grouped := groupedUsageFlags(flags)
	categoryOrder := make([]string, 0, len(flagCategorySpecs)+1)
	for _, category := range flagCategorySpecs {
		categoryOrder = append(categoryOrder, category.Name)
	}
	categoryOrder = append(categoryOrder, flagCategoryOther)

	groups := make([]selfhelp.FlagGroup, 0, len(categoryOrder))
	for _, category := range categoryOrder {
		registered := grouped[category]
		if len(registered) == 0 {
			continue
		}
		group := selfhelp.FlagGroup{Name: category, Flags: make([]selfhelp.Flag, 0, len(registered))}
		for _, flag := range registered {
			group.Flags = append(group.Flags, selfhelp.Flag{
				Name:        flag.Name,
				Shorthand:   flag.Shorthand,
				ValueType:   selfHelpFlagValueType(flag),
				Description: flag.Usage,
				Advanced:    flagIsAdvanced(flag),
			})
		}
		groups = append(groups, group)
	}
	return groups
}

func selfHelpFlagValueType(flag *pflag.Flag) string {
	valueType := strings.TrimSpace(flag.Value.Type())
	switch valueType {
	case "", "bool":
		return ""
	case "stringArray":
		return "string"
	case "int64":
		return "integer"
	default:
		return valueType
	}
}
