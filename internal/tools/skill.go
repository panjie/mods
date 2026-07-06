package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
)

// SkillFileMaxBytes caps how much of an auxiliary file load_skill reads
// into the conversation. 256 KB is enough for reference markdown and
// most source files while preventing accidental context blowup.
const SkillFileMaxBytes = 256 << 10

// RegisterSkill registers the load_skill tool. skills is the scan result
// the tool will serve at runtime; an empty slice is allowed and produces
// a tool whose Call closure returns a not-found error for any name.
// RegisterSkill is unconditional — the caller decides whether to register
// at all (BuildRegistry skips it when the runtime catalog is empty).
func RegisterSkill(reg *Registry, catalog []skills.Skill) error {
	index := make(map[string]*skills.Skill, len(catalog))
	for i := range catalog {
		index[catalog[i].Name] = &catalog[i]
	}
	return reg.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        "load_skill",
			Description: "Load a skill's full instructions by name, or fetch an auxiliary file from a skill's directory. Available skills are listed in the system prompt under \"Available skills\".",
			InputSchema: objectSchema(map[string]any{
				"name": stringProp("Skill name as listed in the system prompt."),
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
	})
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
	if filepath.IsAbs(file) {
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
