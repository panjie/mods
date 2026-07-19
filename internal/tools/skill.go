package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
)

// SkillFileMaxBytes caps how much of an auxiliary file load_skill reads
// into the conversation. 256 KB is enough for reference markdown and
// most source files while preventing accidental context blowup.
const SkillFileMaxBytes = 256 << 10

const (
	skillSearchDefaultLimit = 10
	skillSearchMaxLimit     = 20
	skillSearchMaxBytes     = 4096
)

// RegisterSkill registers load_skill and search_skills. skills is the scan
// result the tools serve at runtime; an empty slice is allowed and produces
// discovery tools with no results.
// RegisterSkill is unconditional — the caller decides whether to register
// at all (BuildRegistry skips it when the runtime catalog is empty).
func RegisterSkill(reg *Registry, catalog []skills.Skill) error {
	index := make(map[string]*skills.Skill, len(catalog))
	for i := range catalog {
		index[catalog[i].Name] = &catalog[i]
	}
	if err := reg.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "load_skill",
			Description: "Load a skill's full instructions by name, or fetch an auxiliary file from its directory. If the name is unknown or absent from the system prompt, call search_skills first.",
			InputSchema: objectSchema(map[string]any{
				"name": stringProp("Exact skill name. Use search_skills first when unknown."),
				"file": stringProp("Optional. Relative path to an auxiliary file inside the skill directory (e.g. 'reference/foo.md', 'scripts/run.py'). Omit to load the skill's SKILL.md body."),
			}, "name"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Name string `json:"name"`
				File string `json:"file"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			skill, ok := index[args.Name]
			if !ok {
				names := make([]string, 0, len(index))
				for n := range index {
					names = append(names, n)
				}
				sort.Strings(names)
				return fmt.Sprintf("skill not found: %s. Available: %s", args.Name, strings.Join(names, ", ")), nil
			}
			if args.File == "" {
				return skill.Body, nil
			}
			content, err := readAuxFile(skill.Dir, args.File)
			if err != nil {
				return err.Error(), nil
			}
			return content, nil
		},
	}); err != nil {
		return err
	}
	return reg.Register(newSearchSkillsTool(catalog))
}

type skillSearchMatch struct {
	skill skills.Skill
	rank  int
}

func newSearchSkillsTool(catalog []skills.Skill) Tool {
	snapshot := append([]skills.Skill(nil), catalog...)
	return Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "search_skills",
			Description: "Search installed skills by name and description. Returns bounded metadata only; use load_skill with an exact result name to load instructions.",
			InputSchema: objectSchema(map[string]any{
				"query": stringProp("Required non-empty search terms matched case-insensitively against skill names and descriptions."),
				"limit": integerProp("Optional maximum number of results. Defaults to 10; allowed range is 1 to 20."),
			}, "query"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
				Limit int    `json:"limit"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", fmt.Errorf("query must not be empty")
			}
			if args.Limit == 0 {
				args.Limit = skillSearchDefaultLimit
			}
			if args.Limit < 1 || args.Limit > skillSearchMaxLimit {
				return "", fmt.Errorf("limit must be between 1 and %d", skillSearchMaxLimit)
			}
			return renderSkillSearch(searchSkills(snapshot, query, args.Limit)), nil
		},
	}
}

func searchSkills(catalog []skills.Skill, query string, limit int) []skillSearchMatch {
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	tokens := strings.Fields(lowerQuery)
	matches := make([]skillSearchMatch, 0)
	for _, skill := range catalog {
		name := strings.ToLower(skill.Name)
		description := strings.ToLower(skill.Description)
		combined := name + " " + description
		matched := true
		for _, token := range tokens {
			if !strings.Contains(combined, token) {
				matched = false
				break
			}
		}
		if !matched {
			continue
		}
		rank := 3
		switch {
		case name == lowerQuery:
			rank = 0
		case strings.HasPrefix(name, lowerQuery):
			rank = 1
		default:
			allName := true
			for _, token := range tokens {
				if !strings.Contains(name, token) {
					allName = false
					break
				}
			}
			if allName {
				rank = 2
			}
		}
		matches = append(matches, skillSearchMatch{skill: skill, rank: rank})
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].rank != matches[j].rank {
			return matches[i].rank < matches[j].rank
		}
		return matches[i].skill.Name < matches[j].skill.Name
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

func renderSkillSearch(matches []skillSearchMatch) string {
	if len(matches) == 0 {
		return "No matching skills found."
	}
	var sb strings.Builder
	for _, match := range matches {
		line := "- " + match.skill.Name + ": " + skills.BoundedDescription(match.skill.Description) + "\n"
		if sb.Len()+len(line) > skillSearchMaxBytes {
			break
		}
		sb.WriteString(line)
	}
	if sb.Len() == 0 {
		return "Matching skill names are too large to display."
	}
	return strings.TrimSuffix(sb.String(), "\n")
}

// readAuxFile reads an auxiliary file from a skill directory with path
// validation and a size cap. Returns an error string suitable for tool
// output (no Go error — the tool returns the message as content so the
// LLM can self-correct).
func readAuxFile(skillDir, file string) (string, error) {
	if file == "" {
		return "", fmt.Errorf("invalid file path: %s", file)
	}
	// Reject absolute paths.
	if pathutil.IsAbs(file) {
		return "", fmt.Errorf("invalid file path: %s", file)
	}
	cleaned := filepath.Clean(file)
	// Reject any '..' path components. filepath.SplitList splits on the
	// PATH-list separator (':' on Unix), NOT path components, so it cannot
	// be used here. Split on '/' (after normalizing to slash form) to walk
	// components cross-platform.
	for _, part := range strings.Split(filepath.ToSlash(cleaned), "/") {
		if part == ".." {
			return "", fmt.Errorf("invalid file path: %s", file)
		}
	}
	resolved := filepath.Join(skillDir, cleaned)
	// Defense-in-depth: resolved path must stay inside skillDir.
	absSkillDir, err := filepath.Abs(skillDir)
	if err == nil {
		absResolved, rerr := filepath.Abs(resolved)
		if rerr == nil && !strings.HasPrefix(absResolved+string(filepath.Separator), absSkillDir+string(filepath.Separator)) && absResolved != absSkillDir {
			return "", fmt.Errorf("invalid file path: %s", file)
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("could not read file: %s: %v", file, err)
	}
	if info.Size() > SkillFileMaxBytes {
		return "", fmt.Errorf("file too large: %s (%d bytes, limit %d)", file, info.Size(), SkillFileMaxBytes)
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return "", fmt.Errorf("could not read file: %s: %v", file, err)
	}
	return string(data), nil
}
