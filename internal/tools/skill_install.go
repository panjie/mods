package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
)

// SourceCache holds the lazily-populated remote skill catalog shared between
// search_skills and install_skill. synced distinguishes "sync ran and found
// nothing" from "sync has not run yet".
type SourceCache struct {
	catalog []skills.SourceSkill
	synced  bool
}

// NewSourceCache returns an empty, not-yet-synced cache.
func NewSourceCache() *SourceCache { return &SourceCache{} }

// SkillInstallConfig configures the search_skills and install_skill tools.
type SkillInstallConfig struct {
	Sources   []skills.Source
	CacheDir  string // where shallow clones live
	SkillsDir string // local install destination
	Cache     *SourceCache
}

// RegisterSearchSkill registers the read-only search_skills tool.
func RegisterSearchSkill(reg *Registry, cfg SkillInstallConfig) error {
	return reg.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "search_skills",
			Description: "Search remote skill sources for installable skills matching a query. Sources are configured via the \"skill-sources\" config key. Returns each match's name, description, and source. Call install_skill to install one.",
			InputSchema: objectSchema(map[string]any{
				"query": stringProp("Keywords to match against skill name and description (case-insensitive)."),
			}, "query"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Query string `json:"query"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			cat, err := ensureSourceCache(ctx, cfg)
			if err != nil {
				return err.Error(), nil
			}
			matches := skills.Search(cat, args.Query, 10)
			if len(matches) == 0 {
				if args.Query == "" {
					return fmt.Sprintf("no skills found in %d source(s)", len(cfg.Sources)), nil
				}
				return fmt.Sprintf("no skills found for %q", args.Query), nil
			}
			return renderSearchResults(matches, cfg.SkillsDir), nil
		},
	})
}

// RegisterInstallSkill registers install_skill. It is Mutable (forces review in
// auto/always review modes) and reports a write intent on the skills dir so
// the review banner shows the target.
func RegisterInstallSkill(reg *Registry, cfg SkillInstallConfig) error {
	skillsDir := cfg.SkillsDir
	return reg.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{Mutable: true},
		IntentExtractor: func(json.RawMessage) approval.AccessIntent {
			return approval.AccessIntent{Class: approval.AccessWrite, Dirs: []string{skillsDir}}
		},
		Spec: proto.ToolSpec{
			Name:        "install_skill",
			Description: "Install a skill from a configured remote source into the local skills directory and return its instructions. The user must approve. After install the skill is also available to load_skill in future sessions.",
			InputSchema: objectSchema(map[string]any{
				"name":   stringProp("Skill name as returned by search_skills."),
				"source": stringProp("Optional. Source repo short name or URL to disambiguate when the same name exists in multiple sources. Omit when unique."),
			}, "name"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Name   string `json:"name"`
				Source string `json:"source"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			cat, err := ensureSourceCache(ctx, cfg)
			if err != nil {
				return err.Error(), nil
			}
			matches := findSkillByName(cat, args.Name, args.Source)
			switch len(matches) {
			case 0:
				return fmt.Sprintf("skill not found in sources: %s. Use search_skills to find available skills.", args.Name), nil
			case 1:
				skill, err := skills.Install(matches[0], skillsDir)
				if err != nil {
					return fmt.Sprintf("could not install skill: %v", err), nil
				}
				return skill.Body, nil
			default:
				var sb strings.Builder
				fmt.Fprintf(&sb, "multiple skills named %q exist; specify a source:\n", args.Name)
				for _, m := range matches {
					fmt.Fprintf(&sb, "- %s (source: %s)\n", m.Name, repoShort(m.Source.URL))
				}
				return strings.TrimRight(sb.String(), "\n"), nil
			}
		},
	})
}

// ensureSourceCache syncs and scans the configured sources on first use, then
// returns the cached catalog for the rest of the session.
func ensureSourceCache(ctx context.Context, cfg SkillInstallConfig) ([]skills.SourceSkill, error) {
	if cfg.Cache.synced {
		return cfg.Cache.catalog, nil
	}
	clones, err := skills.SyncSources(ctx, cfg.CacheDir, cfg.Sources)
	if err != nil {
		return nil, err
	}
	cfg.Cache.catalog = skills.ScanSources(clones)
	cfg.Cache.synced = true
	return cfg.Cache.catalog, nil
}

func findSkillByName(cat []skills.SourceSkill, name, source string) []skills.SourceSkill {
	var out []skills.SourceSkill
	for _, s := range cat {
		if s.Name != name {
			continue
		}
		if source != "" && !sourceMatches(s.Source, source) {
			continue
		}
		out = append(out, s)
	}
	return out
}

func sourceMatches(src skills.Source, want string) bool {
	if src.URL == want || repoShort(src.URL) == want {
		return true
	}
	return strings.Contains(src.URL, want)
}

func renderSearchResults(matches []skills.SourceSkill, skillsDir string) string {
	var sb strings.Builder
	for _, m := range matches {
		fmt.Fprintf(&sb, "- %s — %s (source: %s)", m.Name, m.Description, repoShort(m.Source.URL))
		if skillInstalled(skillsDir, m.Name) {
			sb.WriteString(" (installed)")
		}
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

func skillInstalled(skillsDir, name string) bool {
	info, err := os.Stat(filepath.Join(skillsDir, name))
	return err == nil && info.IsDir()
}

func repoShort(url string) string {
	s := url
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, ".git")
}
