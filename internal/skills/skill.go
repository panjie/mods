// Package skills scans user-defined skill directories and renders the
// system-prompt catalog. A skill is a directory under a configured
// skills directory containing a SKILL.md file with YAML frontmatter (name,
// description) and a markdown body. Unknown frontmatter fields are
// ignored so mods stays compatible with the broader Claude Skills
// ecosystem (license, requires, etc.) without understanding them.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/textutil"
)

const (
	// MaxDescriptionBytes bounds each description placed in a provider prompt
	// or tool result. The in-memory Skill remains unchanged.
	MaxDescriptionBytes = 256
	// MaxCatalogBytes bounds the always-on skill catalog prompt.
	MaxCatalogBytes = 4096
)

// Skill is the parsed result of one skill directory.
type Skill struct {
	Name        string // frontmatter.name, or directory name fallback
	Description string // frontmatter.description, or "(no description)" fallback
	Body        string // markdown body after frontmatter (content of SKILL.md)
	Dir         string // path of the skill directory as scanned (for auxiliary file reads)
}

// Scan walks dir for */SKILL.md (one level, non-recursive) and returns
// skills sorted by Name. Parse failures are skipped with a debug warning;
// other skills continue. Returns nil, nil if dir does not exist or
// contains no SKILL.md.
func Scan(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var found []Skill
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillPath := filepath.Join(dir, entry.Name(), "SKILL.md")
		data, readErr := os.ReadFile(skillPath)
		if readErr != nil {
			continue // no SKILL.md in this directory; skip silently
		}
		skill, parseErr := parseSkill(string(data), filepath.Join(dir, entry.Name()))
		if parseErr != nil {
			debug.Printf("skills: skipping %q: %v", entry.Name(), parseErr)
			continue
		}
		found = append(found, skill)
	}
	if len(found) == 0 {
		return nil, nil
	}
	// Deduplicate by Name: later entries (lexical directory order) overwrite
	// earlier ones. Warn on collision.
	byName := make(map[string]int, len(found))
	result := found[:0]
	for _, s := range found {
		if idx, ok := byName[s.Name]; ok {
			debug.Printf("skills: name collision %q (dir %q overwrites %q)", s.Name, s.Dir, found[idx].Dir)
			result[idx] = s
			continue
		}
		byName[s.Name] = len(result)
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// ScanDirs scans multiple skills directories, with later directories
// overriding earlier same-name skills. The returned catalog is sorted by name.
func ScanDirs(dirs []string) ([]Skill, error) {
	byName := make(map[string]Skill)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		catalog, err := Scan(dir)
		if err != nil {
			return nil, err
		}
		for _, skill := range catalog {
			if prev, ok := byName[skill.Name]; ok {
				debug.Printf("skills: name collision %q (dir %q overwrites %q)", skill.Name, skill.Dir, prev.Dir)
			}
			byName[skill.Name] = skill
		}
	}
	if len(byName) == 0 {
		return nil, nil
	}
	result := make([]Skill, 0, len(byName))
	for _, skill := range byName {
		result = append(result, skill)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

// parseSkill parses SKILL.md content into a Skill. dir is the absolute
// path of the skill directory as scanned (stored on Skill.Dir for auxiliary
// file reads). The directory name is the fallback for a missing frontmatter
// name.
func parseSkill(content, dir string) (Skill, error) {
	skill := Skill{Dir: dir}
	name, desc, body, ok := splitFrontmatter(content)
	if !ok {
		// No frontmatter; whole file is body, name from directory.
		skill.Name = filepath.Base(dir)
		skill.Description = "(no description)"
		skill.Body = strings.TrimSpace(content)
		return skill, nil
	}
	skill.Name = strings.TrimSpace(name)
	skill.Description = strings.TrimSpace(desc)
	skill.Body = strings.TrimSpace(body)
	if skill.Name == "" {
		skill.Name = filepath.Base(dir)
	}
	if skill.Description == "" {
		skill.Description = "(no description)"
	}
	return skill, nil
}

// splitFrontmatter extracts the YAML frontmatter block from content.
// Returns (name, description, body, ok). ok is false when content does
// not start with a frontmatter delimiter. The parser only reads the two
// fields mods cares about (name, description); all other lines in the
// block are ignored so unknown fields (license, requires, ...) don't
// break parsing.
func splitFrontmatter(content string) (name, description, body string, ok bool) {
	const marker = "---"
	// Frontmatter must be the very first thing in the file.
	if !strings.HasPrefix(content, marker+"\n") && content != marker {
		return "", "", "", false
	}
	rest := strings.TrimPrefix(content, marker+"\n")
	rest = strings.TrimPrefix(rest, marker)
	// Find the closing marker on its own line.
	lines := strings.Split(rest, "\n")
	closeIdx := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == marker {
			closeIdx = i
			break
		}
	}
	if closeIdx == -1 {
		// Unterminated frontmatter; caller treats whole file as body.
		return "", "", "", false
	}
	fmBlock := lines[:closeIdx]
	bodyLines := lines[closeIdx+1:]
	body = strings.Join(bodyLines, "\n")
	for _, line := range fmBlock {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		// Strip surrounding quotes if present.
		value = strings.Trim(value, "\"'")
		switch key {
		case "name":
			name = value
		case "description":
			description = value
		}
	}
	return name, description, body, true
}

// CatalogRender describes a bounded catalog prompt.
type CatalogRender struct {
	Prompt   string
	Included int
	Omitted  int
}

// BoundedDescription returns a UTF-8-safe prompt representation without
// changing the full description held in memory.
func BoundedDescription(description string) string {
	if len(description) <= MaxDescriptionBytes {
		return description
	}
	const suffix = "..."
	return textutil.TruncateUTF8Bytes(description, MaxDescriptionBytes-len(suffix)) + suffix
}

// CatalogPrompt renders the system-prompt section listing available skills
// within the hard catalog limit.
func CatalogPrompt(skills []Skill) string {
	return CatalogPromptBudget(skills, MaxCatalogBytes).Prompt
}

// CatalogPromptBudget renders a deterministically sorted catalog within both
// maxBytes and MaxCatalogBytes. A budget too small for useful discovery
// guidance produces an empty prompt; search_skills remains available.
func CatalogPromptBudget(catalog []Skill, maxBytes int) CatalogRender {
	if len(catalog) == 0 || maxBytes <= 0 {
		return CatalogRender{Omitted: len(catalog)}
	}
	if maxBytes > MaxCatalogBytes {
		maxBytes = MaxCatalogBytes
	}
	sorted := append([]Skill(nil), catalog...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })

	const header = "## Available skills\n\n" +
		"Call load_skill(<name>) to load full instructions. If the name is unknown or omitted below, call search_skills first.\n"
	if len(header) > maxBytes {
		return CatalogRender{Omitted: len(sorted)}
	}

	lines := make([]string, 0, len(sorted))
	for _, skill := range sorted {
		line := "- " + skill.Name + ": " + BoundedDescription(skill.Description) + "\n"
		omittedAfter := len(sorted) - len(lines) - 1
		candidate := header + strings.Join(append(append([]string(nil), lines...), line), "")
		if omittedAfter > 0 {
			candidate += catalogOmittedNotice(omittedAfter)
		}
		if len(candidate) > maxBytes {
			break
		}
		lines = append(lines, line)
	}

	result := CatalogRender{Included: len(lines), Omitted: len(sorted) - len(lines)}
	result.Prompt = header + strings.Join(lines, "")
	if result.Omitted > 0 {
		notice := catalogOmittedNotice(result.Omitted)
		if len(result.Prompt)+len(notice) <= maxBytes {
			result.Prompt += notice
		} else if len(lines) == 0 {
			// A header without either entries or an omission hint is misleading.
			return CatalogRender{Omitted: len(sorted)}
		}
	}
	return result
}

func catalogOmittedNotice(count int) string {
	return "- ... " + fmt.Sprintf("%d more skills omitted; use search_skills.\n", count)
}
