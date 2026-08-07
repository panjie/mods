package skills

import (
	_ "embed"
	"sort"
)

//go:embed builtin/cross-platform-command-execution/SKILL.md
var crossPlatformCommandExecution string

// Builtin returns the skills shipped in the mods binary. Built-ins are the
// lowest-precedence catalog layer; user-installed skills may override them by
// name without modifying the executable.
func Builtin() []Skill {
	skill, err := parseSkill(crossPlatformCommandExecution, "builtin/cross-platform-command-execution")
	if err != nil {
		return nil
	}
	skill.Dir = ""
	return []Skill{skill}
}

// MergeCatalog overlays catalogs from left to right by skill name and returns
// a stable, sorted snapshot.
func MergeCatalog(catalogs ...[]Skill) []Skill {
	byName := make(map[string]Skill)
	for _, catalog := range catalogs {
		for _, skill := range catalog {
			byName[skill.Name] = skill
		}
	}
	result := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
