package skills

import (
	_ "embed"
	"sort"
)

//go:embed builtin/cross-platform-command-execution/SKILL.md
var crossPlatformCommandExecution string

//go:embed builtin/diagnose-not-reproducing/SKILL.md
var diagnoseNotReproducing string

//go:embed builtin/code-metrics/SKILL.md
var codeMetrics string

//go:embed builtin/configure-mods/SKILL.md
var configureMods string

// Builtin returns the skills shipped in the mods binary. Built-ins are the
// lowest-precedence catalog layer; user-installed skills may override them by
// name without modifying the executable.
func Builtin() []Skill {
	builtinSources := []struct {
		dir  string
		body string
	}{
		{"builtin/cross-platform-command-execution", crossPlatformCommandExecution},
		{"builtin/diagnose-not-reproducing", diagnoseNotReproducing},
		{"builtin/code-metrics", codeMetrics},
		{"builtin/configure-mods", configureMods},
	}
	skills := make([]Skill, 0, len(builtinSources))
	for _, source := range builtinSources {
		skill, err := parseSkill(source.body, source.dir)
		if err != nil {
			continue
		}
		skill.Dir = ""
		skills = append(skills, skill)
	}
	return skills
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
