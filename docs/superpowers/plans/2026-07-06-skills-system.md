# Skills System Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a built-in skills system to mods so the LLM can autonomously load user-defined skill instructions (markdown + frontmatter) from `~/.config/mods/skills/`, compatible with the awesome-claude-skills format.

**Architecture:** `internal/skills.Scan()` walks `~/.config/mods/skills/*/SKILL.md` once per conversation, parses YAML frontmatter (`name`, `description`) with a hand-rolled parser, and caches the result on `Mods.skillCatalog`. The catalog is injected into the system prompt and a `load_skill(name, file?)` tool is registered. The LLM calls the tool to load a skill's body or auxiliary files from `scripts/`/`reference/` subdirectories.

**Tech Stack:** Go 1.22+, `github.com/adrg/xdg` (already a dependency), `github.com/panjie/mods/internal/{config,proto,tools,app,prompts,debug}`. No new external dependencies.

## Global Constraints

- No new external dependencies. Hand-rolled frontmatter parser (no `gopkg.in/yaml.v3`).
- Tool naming follows existing convention: snake_case (`load_skill`, matching `fs_read_file`, `web_search`, `shell_run`, `thinking_note`).
- Config precedence: CLI flags > mods.yml > MODS_ env > defaults. `skills-dir` uses yaml tag `skills-dir` + env `MODS_SKILLS_DIR`.
- Portable mode: `skills-dir` default is `<exe-dir>/skills` when portable mode is active, else `xdg.ConfigHome/mods/skills`.
- No tilde expansion on user-supplied values (matches all other path-valued config keys).
- Skills are pure prompt content; `requires`/`license`/other frontmatter fields are parsed-but-ignored.
- `load_skill` is `ReadOnly: true`, does not go through `requestApproval`.
- Catalog empty → no system-prompt injection, no tool registration (zero overhead).
- Minimal mode → no catalog injection (matches identity.md skipping).
- Tests: `go test ./...` must pass. Lint: `golangci-lint run ./...` if installed.
- AGENTS.md note: when adding a config-file-only key, update config struct + `Help` map + `config_template.yml` + config tests + `internal/prompts/identity.md` together. This plan does all five for `skills-dir`.

**Spec:** `docs/superpowers/specs/2026-07-06-skills-system-design.md`

---

## File Structure

**New files:**
- `internal/skills/skill.go` — `Skill` struct, `Scan()`, frontmatter parser, `CatalogPrompt()`. One responsibility: read skill dirs and render catalog text.
- `internal/skills/scan_test.go` — tests for parsing, scanning, catalog rendering.
- `internal/tools/skill.go` — `RegisterSkill()` + `load_skill` tool (body + auxiliary file loading). One responsibility: expose skills via a tool.
- `internal/tools/skill_test.go` — tests for the tool.

**Modified files:**
- `internal/config/config.go` — add `SkillsDir` field on `PersistentConfig`, `defaultSkillsDir()`, `applySkillsDirDefault()`, `Help` map entry.
- `internal/config/config_template.yml` — add commented-out `# skills-dir:` key with help text.
- `internal/config/config_test.go` — tests for default/env/YAML override of `skills-dir`.
- `internal/app/mods.go` — add `skillCatalog` field on `Mods`, populate in `New()`.
- `internal/app/stream.go` — inject catalog into `setupStreamContext`.
- `internal/app/stream_test.go` (or `mods_test.go`) — test for catalog injection.
- `internal/tooling/tools.go` — add `skills []skills.Skill` param to `BuildRegistry`, call `RegisterSkill`; update `BuiltinSpecs`.
- `internal/app/tool_support.go` — pass `m.skillCatalog` to `BuildRegistry`.
- `internal/prompts/identity.md` — add `## Skills` section.
- `internal/prompts/prompts_test.go` — add `skills-dir` to `TestIdentityCoversConfigKeys`.

---

### Task 1: Config — `skills-dir` key with default resolution

**Files:**
- Modify: `internal/config/config.go` (add field on `PersistentConfig` ~line 226; add `defaultSkillsDir`/`applySkillsDirDefault` near `defaultSessionDir` ~line 555; add Help entry ~line 47; call `applySkillsDirDefault` in `Ensure` ~line 420 and ~line 467)
- Modify: `internal/config/config_template.yml` (add commented-out key near other optional path keys)
- Test: `internal/config/config_test.go`

**Interfaces:**
- Produces: `Config.SkillsDir string` (yaml `skills-dir`, env `MODS_SKILLS_DIR`), `defaultSkillsDir() string`, `applySkillsDirDefault(*Config)`. Later tasks use `cfg.SkillsDir` as the scan root.

- [ ] **Step 1: Write the failing test for default/env/YAML resolution**

Add to `internal/config/config_test.go`:

```go
func TestSkillsDirDefault(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	cfg := Default()
	applySkillsDirDefault(&cfg)
	require.NotEmpty(t, cfg.SkillsDir)
	require.Contains(t, cfg.SkillsDir, filepath.Join("mods", "skills"))
}

func TestSkillsDirEnvOverride(t *testing.T) {
	t.Setenv("MODS_SKILLS_DIR", "/custom/skills/dir")
	cfg := Default()
	// env is parsed in Ensure; for a unit test, simulate by setting the
	// field the way env.ParseWithOptions would. applySkillsDirDefault
	// must not clobber a non-empty value.
	cfg.SkillsDir = "/custom/skills/dir"
	applySkillsDirDefault(&cfg)
	require.Equal(t, "/custom/skills/dir", cfg.SkillsDir)
}

func TestSkillsDirYAMLOverride(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, "from-yaml")
	yamlContent := "skills-dir: " + skillsDir
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(yamlContent), &cfg))
	applySkillsDirDefault(&cfg)
	require.Equal(t, skillsDir, cfg.SkillsDir)
}
```

Add imports if missing: `"path/filepath"`, `"github.com/panjie/mods/internal/config"` (if referencing `Default`), `yaml "gopkg.in/yaml.v3"`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config -run TestSkillsDir -count=1 -v`
Expected: FAIL — `cfg.SkillsDir` undefined, `defaultSkillsDir`/`applySkillsDirDefault` undefined.

- [ ] **Step 3: Add the `SkillsDir` field to `PersistentConfig`**

In `internal/config/config.go`, find the `PersistentConfig` struct. After the `ShellClassifyPrompt` field (~line 225), add:

```go
	SkillsDir          string                     `yaml:"skills-dir" env:"SKILLS_DIR"`
```

Place it in alphabetical-ish position near `ShellClassifyPrompt` to match the existing ordering style.

- [ ] **Step 4: Add `defaultSkillsDir` and `applySkillsDirDefault`**

In `internal/config/config.go`, immediately after the existing `defaultSessionDir()` function (~line 564), add:

```go
// defaultSkillsDir resolves the user-defined skills directory. In portable
// mode (mods.yml next to the executable) it lives next to the binary so the
// whole folder is self-contained; otherwise it follows the XDG config home,
// sitting beside mods.yml. Mirrors defaultSessionDir.
func defaultSkillsDir() string {
	if portableActive() {
		return filepath.Join(executableDir(), "skills")
	}
	return filepath.Join(xdg.ConfigHome, "mods", "skills")
}

// applySkillsDirDefault ensures c.SkillsDir always points at the skills
// directory. The default lives outside Default() for the same reason as
// applySessionDirDefault: the XDG lookup depends on environment variables
// resolved at Ensure() time.
func applySkillsDirDefault(c *Config) {
	if c.SkillsDir == "" {
		c.SkillsDir = defaultSkillsDir()
	}
}
```

- [ ] **Step 5: Call `applySkillsDirDefault` in `Ensure`**

In `internal/config/config.go` `Ensure()`, find the two calls to `applySessionDirDefault(&c)` / `applySessionDirDefault(&fallback)` (~lines 420-421) and add after each:

```go
	applySkillsDirDefault(&c)
	applySkillsDirDefault(&fallback)
```

And after the second call site (~line 467, after `applySessionDirDefault(&c)` post-YAML-parse), add:

```go
	applySkillsDirDefault(&c)
```

- [ ] **Step 6: Add the `Help` map entry**

In `internal/config/config.go`, find `var Help = map[string]string{` (~line 47). Add a new entry (place near `shell-classify-prompt` to group with other path-ish keys, or alphabetically):

```go
	"skills-dir":             "Directory containing user-defined skills (one subdirectory per skill, each with a SKILL.md). Defaults to ~/.config/mods/skills (or next to the executable in portable mode).",
```

- [ ] **Step 7: Add the commented-out key to the config template**

In `internal/config/config_template.yml`, find the `# web-search-api-key:` block (~line 142-144). After it, add:

```yaml

# {{ index .Help "skills-dir" }}
# skills-dir:
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `go test ./internal/config -run TestSkillsDir -count=1 -v`
Expected: PASS (all three subtests).

- [ ] **Step 9: Run the full config package tests + build**

Run: `go test ./internal/config -count=1` && `go build ./...`
Expected: PASS, no build errors.

- [ ] **Step 10: Commit**

```bash
git add internal/config/config.go internal/config/config_template.yml internal/config/config_test.go
git commit -m "feat(config): add skills-dir config key with portable-mode default"
```

---

### Task 2: Skills package — `Skill` struct, frontmatter parser, `Scan`

**Files:**
- Create: `internal/skills/skill.go`
- Test: `internal/skills/scan_test.go`

**Interfaces:**
- Consumes: nothing (standalone package).
- Produces: `skills.Skill{Name, Description, Body, Dir}`, `skills.Scan(dir string) ([]Skill, error)`. Later tasks call `Scan(cfg.SkillsDir)` and pass the result to `RegisterSkill` and `CatalogPrompt`.

- [ ] **Step 1: Write the failing test for parsing + scanning**

Create `internal/skills/scan_test.go`:

```go
package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeSkill writes a SKILL.md inside dir/<name>/ with the given content.
func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600))
}

func TestScanNormal(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "---\nname: alpha\ndescription: Alpha skill.\n\nBody text.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "alpha", skills[0].Name)
	require.Equal(t, "Alpha skill.", skills[0].Description)
	require.Equal(t, "Body text.", skills[0].Body)
	require.Contains(t, skills[0].Dir, "alpha")
}

func TestScanMissingNameFallsBackToDirName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "beta", "---\ndescription: Beta skill.\n\nBody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "beta", skills[0].Name)
	require.Equal(t, "Beta skill.", skills[0].Description)
}

func TestScanMissingDescriptionUsesPlaceholder(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "gamma", "---\nname: gamma\n\nBody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "gamma", skills[0].Name)
	require.Equal(t, "(no description)", skills[0].Description)
}

func TestScanNoFrontmatterWholeFileIsBody(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "delta", "# Delta\n\nJust a body, no frontmatter.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "delta", skills[0].Name)
	require.Equal(t, "(no description)", skills[0].Description)
	require.Contains(t, skills[0].Body, "Just a body")
}

func TestScanUnknownFrontmatterFieldsIgnored(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: epsilon\ndescription: Epsilon.\nlicense: MIT\nrequires:\n  mcp: [rube]\n---\nBody.\n"
	writeSkill(t, root, "epsilon", content)
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "epsilon", skills[0].Name)
	require.Equal(t, "Epsilon.", skills[0].Description)
	require.Equal(t, "Body.", skills[0].Body)
}

func TestScanUnterminatedFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "zeta", "---\nname: zeta\n\ndescription: never closed\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "zeta", skills[0].Name) // falls back to dir name
	require.Contains(t, skills[0].Body, "name: zeta") // whole file is body
}

func TestScanNameCollisionLaterWins(t *testing.T) {
	root := t.TempDir()
	// Two directories whose frontmatter name resolves to the same value.
	writeSkill(t, root, "dir-a", "---\nname: same\n\nBody A.\n")
	writeSkill(t, root, "dir-b", "---\nname: same\n\nBody B.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	// Directory walk order is lexical, so dir-b is processed last and wins.
	require.Equal(t, "Body B.", skills[0].Body)
}

func TestScanSortedByName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "charlie", "---\nname: charlie\n\nc.\n")
	writeSkill(t, root, "alpha", "---\nname: alpha\n\na.\n")
	writeSkill(t, root, "bravo", "---\nname: bravo\n\nb.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 3)
	require.Equal(t, "alpha", skills[0].Name)
	require.Equal(t, "bravo", skills[1].Name)
	require.Equal(t, "charlie", skills[2].Name)
}

func TestScanEmptyDirReturnsNilNoError(t *testing.T) {
	root := t.TempDir()
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Nil(t, skills)
}

func TestScanNonexistentDirReturnsNilNoError(t *testing.T) {
	skills, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	require.Nil(t, skills)
}

func TestScanSkipsDirsLackingSkillMd(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "no-skill"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "no-skill", "README.md"), []byte("hi"), 0o600))
	writeSkill(t, root, "real", "---\nname: real\n\nbody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "real", skills[0].Name)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills -count=1 -v`
Expected: FAIL — package doesn't exist, `Scan` undefined.

- [ ] **Step 3: Create the skills package with `Skill`, `Scan`, and the frontmatter parser**

Create `internal/skills/skill.go`:

```go
// Package skills scans user-defined skill directories and renders the
// system-prompt catalog. A skill is a directory under the configured
// skills-dir containing a SKILL.md file with YAML frontmatter (name,
// description) and a markdown body. Unknown frontmatter fields are
// ignored so mods stays compatible with the broader Claude Skills
// ecosystem (license, requires, etc.) without understanding them.
package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/panjie/mods/internal/debug"
)

// Skill is the parsed result of one skill directory.
type Skill struct {
	Name        string // frontmatter.name, or directory name fallback
	Description string // frontmatter.description, or "(no description)" fallback
	Body        string // markdown body after frontmatter (content of SKILL.md)
	Dir         string // absolute path of the skill directory (for auxiliary file reads)
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

// parseSkill parses SKILL.md content into a Skill. dir is the absolute
// path of the skill directory (stored on Skill.Dir for auxiliary file
// reads). The directory name is the fallback for a missing frontmatter
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/skills -count=1 -v`
Expected: PASS (all 11 subtests).

- [ ] **Step 5: Commit**

```bash
git add internal/skills/skill.go internal/skills/scan_test.go
git commit -m "feat(skills): add Skill struct, frontmatter parser, and Scan"
```

---

### Task 3: Skills package — `CatalogPrompt` renderer

**Files:**
- Modify: `internal/skills/skill.go` (append `CatalogPrompt`)
- Test: `internal/skills/scan_test.go` (append tests)

**Interfaces:**
- Consumes: `[]Skill` from Task 2.
- Produces: `skills.CatalogPrompt(skills []Skill) string`. Later tasks call this in `setupStreamContext`.

- [ ] **Step 1: Write the failing test for `CatalogPrompt`**

Append to `internal/skills/scan_test.go`:

```go
func TestCatalogPromptEmptyReturnsEmpty(t *testing.T) {
	require.Equal(t, "", CatalogPrompt(nil))
	require.Equal(t, "", CatalogPrompt([]Skill{}))
}

func TestCatalogPromptFormat(t *testing.T) {
	skills := []Skill{
		{Name: "alpha", Description: "Alpha skill."},
		{Name: "bravo", Description: "Bravo skill."},
	}
	got := CatalogPrompt(skills)
	require.Contains(t, got, "## Available skills")
	require.Contains(t, got, "load_skill")
	require.Contains(t, got, "- alpha: Alpha skill.")
	require.Contains(t, got, "- bravo: Bravo skill.")
	// Alpha must appear before bravo (caller passes pre-sorted slice).
	require.Less(t, strings.Index(got, "alpha"), strings.Index(got, "bravo"))
}
```

Add `"strings"` to the test file's imports if not already present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/skills -run TestCatalogPrompt -count=1 -v`
Expected: FAIL — `CatalogPrompt` undefined.

- [ ] **Step 3: Implement `CatalogPrompt`**

Append to `internal/skills/skill.go`:

```go
// CatalogPrompt renders the system-prompt section listing available skills.
// Returns "" for an empty slice (caller skips injection entirely). The
// caller is expected to pass a slice already sorted by Name (Scan does
// this); CatalogPrompt does not re-sort.
func CatalogPrompt(skills []Skill) string {
	if len(skills) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("## Available skills\n\n")
	sb.WriteString("Call load_skill(<name>) to load a skill's full instructions.\n")
	sb.WriteString("To fetch an auxiliary file (e.g. reference/foo.md), pass it as the optional second argument: load_skill(<name>, \"<file>\").\n")
	for _, s := range skills {
		sb.WriteString("- ")
		sb.WriteString(s.Name)
		sb.WriteString(": ")
		sb.WriteString(s.Description)
		sb.WriteString("\n")
	}
	return sb.String()
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/skills -count=1 -v`
Expected: PASS (all subtests including the two new ones).

- [ ] **Step 5: Commit**

```bash
git add internal/skills/skill.go internal/skills/scan_test.go
git commit -m "feat(skills): add CatalogPrompt system-prompt renderer"
```

---

### Task 4: Tools — `RegisterSkill` + `load_skill` tool (body + auxiliary file)

**Files:**
- Create: `internal/tools/skill.go`
- Test: `internal/tools/skill_test.go`

**Interfaces:**
- Consumes: `skills.Skill` and `skills.Scan` from Tasks 2-3, plus the existing `Registry`, `Tool`, `proto.ToolSpec`, `objectSchema`/`stringProp`/`decodeArgs` helpers in `internal/tools`.
- Produces: `tools.RegisterSkill(reg *Registry, skills []skills.Skill) error`. Called by `BuildRegistry` (Task 5) when the catalog is non-empty, and by `BuiltinSpecs` (Task 5) with nil to harvest the spec.

- [ ] **Step 1: Write the failing test for body loading + auxiliary file loading + path safety**

Create `internal/tools/skill_test.go`:

```go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

// setupSkillFixture builds a temp skills-dir with two skills: one flat,
// one multi-file. Returns the dir and the parsed catalog.
func setupSkillFixture(t *testing.T) (string, []skills.Skill) {
	t.Helper()
	root := t.TempDir()
	// Flat skill.
	flatDir := filepath.Join(root, "flat", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(flatDir), 0o700))
	require.NoError(t, os.WriteFile(flatDir, []byte("---\nname: flat\ndescription: A flat skill.\n\nFlat body.\n"), 0o600))
	// Multi-file skill with reference/ and scripts/.
	multiDir := filepath.Join(root, "multi")
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "reference"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "scripts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "SKILL.md"), []byte("---\nname: multi\ndescription: A multi-file skill.\n\nSee reference/detail.md.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "reference", "detail.md"), []byte("Detail content.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "scripts", "run.py"), []byte("print('hi')\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	require.Len(t, catalog, 2)
	return root, catalog
}

func loadSkillTool(t *testing.T, catalog []skills.Skill) (*Registry, string) {
	t.Helper()
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, catalog))
	// Sanity: tool is registered.
	_, ok := reg.Tool("load_skill")
	require.True(t, ok)
	return reg, ""
}

func callLoadSkill(t *testing.T, reg *Registry, args string) string {
	t.Helper()
	out, err := reg.Call(context.Background(), "load_skill", []byte(args))
	require.NoError(t, err)
	return out
}

func TestLoadSkillBody(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"flat"}`)
	require.Equal(t, "Flat body.", got)
}

func TestLoadSkillBodyMultiFileSkill(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi"}`)
	require.Equal(t, "See reference/detail.md.", got)
}

func TestLoadSkillNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"nonexistent"}`)
	require.Contains(t, got, "skill not found: nonexistent")
	require.Contains(t, got, "Available:")
	require.Contains(t, got, "flat")
	require.Contains(t, got, "multi")
}

func TestLoadSkillNamePathEscapeIsOrdinaryNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	for _, bad := range []string{`..`, `../etc`, `/etc/passwd`, `a/b`} {
		got := callLoadSkill(t, reg, `{"name":"`+bad+`"}`)
		require.Contains(t, got, "skill not found", "input %q", bad)
	}
}

func TestLoadSkillAuxFile(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/detail.md"}`)
	require.Equal(t, "Detail content.\n", got)
}

func TestLoadSkillAuxFileScript(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"scripts/run.py"}`)
	require.Equal(t, "print('hi')\n", got)
}

func TestLoadSkillAuxFilePathEscapeRejected(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	for _, bad := range []string{`../etc/passwd`, `a/../../b`, `/etc/passwd`} {
		args := `{"name":"multi","file":"` + bad + `"}`
		got := callLoadSkill(t, reg, args)
		require.Contains(t, got, "invalid file path", "input %q", bad)
		require.NotContains(t, got, "Detail content", "must not read escaped path %q", bad)
	}
}

func TestLoadSkillAuxFileNonexistent(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/missing.md"}`)
	require.Contains(t, got, "could not read file")
}

func TestLoadSkillAuxFileSkillNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"nonexistent","file":"reference/x.md"}`)
	require.Contains(t, got, "skill not found: nonexistent")
}

func TestLoadSkillEmptyCatalog(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, nil))
	got := callLoadSkill(t, reg, `{"name":"anything"}`)
	require.Contains(t, got, "skill not found: anything")
	require.Contains(t, got, "Available:") // empty list, but header present
}

func TestLoadSkillIdempotentBody(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	first := callLoadSkill(t, reg, `{"name":"flat"}`)
	second := callLoadSkill(t, reg, `{"name":"flat"}`)
	require.Equal(t, first, second)
}

func TestRegisterSkillSpecHarvested(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, nil))
	tool, ok := reg.Tool("load_skill")
	require.True(t, ok)
	require.Equal(t, "load_skill", tool.Spec.Name)
	require.NotEmpty(t, tool.Spec.Description)
	require.True(t, tool.Capabilities.ReadOnly)
	// Verify input schema has name (required) and file (optional).
	props, ok := tool.Spec.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "name")
	require.Contains(t, props, "file")
	required, ok := tool.Spec.InputSchema["required"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"name"}, required)
}

func TestLoadSkillLargeFileRejected(t *testing.T) {
	root := t.TempDir()
	multiDir := filepath.Join(root, "multi", "scripts")
	require.NoError(t, os.MkdirAll(multiDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "multi", "SKILL.md"), []byte("---\nname: multi\ndescription: m.\n\nbody.\n"), 0o600))
	// Write a file just over the 256 KB cap.
	big := strings.Repeat("x", SkillFileMaxBytes+1)
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "big.txt"), []byte(big), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"scripts/big.txt"}`)
	require.Contains(t, got, "file too large")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run TestLoadSkill -count=1 -v`
Expected: FAIL — `RegisterSkill`, `SkillFileMaxBytes` undefined.

- [ ] **Step 3: Implement `RegisterSkill` and the `load_skill` tool**

Create `internal/tools/skill.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	// Reject any '..' components.
	for _, part := range filepath.SplitList(cleaned) {
		if part == ".." {
			return "", fmt.Errorf("invalid file path: %s", file)
		}
	}
	if strings.Contains(filepath.ToSlash(cleaned), "/../") || cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid file path: %s", file)
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tools -run TestLoadSkill -count=1 -v && go test ./internal/tools -run TestRegisterSkillSpecHarvested -count=1 -v`
Expected: PASS (all subtests).

- [ ] **Step 5: Run the full tools package tests**

Run: `go test ./internal/tools -count=1`
Expected: PASS (no regressions in existing tool tests).

- [ ] **Step 6: Commit**

```bash
git add internal/tools/skill.go internal/tools/skill_test.go
git commit -m "feat(tools): add load_skill tool with body and auxiliary file loading"
```

---

### Task 5: Tooling — wire `RegisterSkill` into `BuildRegistry` and `BuiltinSpecs`

**Files:**
- Modify: `internal/tooling/tools.go` (add `skills` param to `BuildRegistry` ~line 18; call `RegisterSkill` after MCP registration ~line 66; update `BuiltinSpecs` ~line 133)
- Modify: `internal/app/tool_support.go` (pass `m.skillCatalog` to `BuildRegistry` ~line 48)
- Modify: `internal/app/aliases.go` (the `BuildRegistry` re-export ~line 56 may need a signature update if it's a type alias)
- Test: `internal/tooling/tools_test.go` (create if it doesn't exist) or an `internal/app` test

**Interfaces:**
- Consumes: `tools.RegisterSkill` and `skills.Skill` from Task 4, `m.skillCatalog` from Task 7 (forward reference — implement the wiring now, populate the field in Task 7).
- Produces: `tooling.BuildRegistry(ctx, cfg, wscfg, prompt, skillCatalog)`. `load_skill` appears in `--list-tools` output via `BuiltinSpecs`.

- [ ] **Step 1: Write the failing test for `BuildRegistry` wiring**

First check whether `internal/tooling` has tests. If not, create `internal/tooling/tools_test.go`:

```go
package tooling

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestBuildRegistryRegistersLoadSkillWhenCatalogNonEmpty(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillDir), 0o700))
	require.NoError(t, os.WriteFile(skillDir, []byte("---\nname: demo\ndescription: Demo.\n\nbody.\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	require.Len(t, catalog, 1)

	cfg := cfgpkg.Default()
	cfg.SkillsDir = root
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", catalog)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.True(t, ok, "load_skill must be registered when catalog is non-empty")
}

func TestBuildRegistrySkipsLoadSkillWhenCatalogEmpty(t *testing.T) {
	cfg := cfgpkg.Default()
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.False(t, ok, "load_skill must NOT be registered when catalog is empty")
}

func TestBuiltinSpecsIncludesLoadSkill(t *testing.T) {
	specs, err := BuiltinSpecs()
	require.NoError(t, err)
	found := false
	for _, s := range specs {
		if s.Name == "load_skill" {
			found = true
			require.True(t, s.ReadOnly, "load_skill must be ReadOnly")
		}
	}
	require.True(t, found, "load_skill must appear in --list-tools output")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tooling -count=1 -v`
Expected: FAIL — `BuildRegistry` signature mismatch (extra `catalog` arg), or `BuiltinSpecs` doesn't list `load_skill`.

- [ ] **Step 3: Update `BuildRegistry` signature and body**

In `internal/tooling/tools.go`, change the signature:

```go
func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string, skillCatalog []skills.Skill) (*toolregistry.Registry, error) {
```

Add the import: `"github.com/panjie/mods/internal/skills"`.

After the MCP registration block (after line 66 `if err := mcpclient.RegisterTools(ctx, cfg, registry); err != nil {`), add:

```go
	if len(skillCatalog) > 0 {
		if err := toolregistry.RegisterSkill(registry, skillCatalog); err != nil {
			return nil, err
		}
	}
```

- [ ] **Step 4: Update `BuiltinSpecs` to harvest the `load_skill` spec**

In `internal/tooling/tools.go` `BuiltinSpecs()`, after the `_ = toolregistry.RegisterThinking(registry)` line (~line 133), add:

```go
	_ = toolregistry.RegisterSkill(registry, nil)
```

- [ ] **Step 5: Update the `BuildRegistry` caller in `internal/app/tool_support.go`**

In `internal/app/tool_support.go` line 48, change:

```go
		return BuildRegistry(ctx, cfg, wscfg, prompt)
```

to:

```go
		return BuildRegistry(ctx, cfg, wscfg, prompt, m.skillCatalog)
```

- [ ] **Step 6: Update the `BuildRegistry` re-export in `internal/app/aliases.go`**

Check `internal/app/aliases.go` line 56. If it's a var assignment like `var BuildRegistry = tooling.BuildRegistry`, the signature update is automatic (it's a function value). Verify it still compiles — no change needed unless it's a wrapper. Run the build to confirm.

- [ ] **Step 7: Run the tooling tests**

Run: `go test ./internal/tooling -count=1 -v`
Expected: PASS (all three subtests).

- [ ] **Step 8: Build the whole project (will fail until Task 7 adds `m.skillCatalog`)**

Run: `go build ./...`
Expected: FAIL — `m.skillCatalog` undefined on `*Mods`. This is expected; Task 7 adds the field. Commit the tooling changes now (the build failure is isolated to the `app` package referencing the not-yet-added field — but since we can't commit a broken build, instead defer the `tool_support.go` change to Task 7).

**Revised step:** Do NOT change `tool_support.go` yet. Leave the `m.skillCatalog` reference for Task 7. Instead, verify tooling tests pass independently:

Run: `go test ./internal/tooling -count=1 -v`
Expected: PASS.

Run: `go build ./internal/tooling ./internal/tools ./internal/skills ./internal/config`
Expected: PASS (these packages don't depend on `m.skillCatalog`).

- [ ] **Step 9: Commit (tooling only; app wiring deferred to Task 7)**

```bash
git add internal/tooling/tools.go internal/tooling/tools_test.go
git commit -m "feat(tooling): wire RegisterSkill into BuildRegistry and BuiltinSpecs"
```

---

### Task 6: App — `Mods.skillCatalog` field + populate in `New()` + inject in `setupStreamContext`

**Files:**
- Modify: `internal/app/mods.go` (add field ~line 122; populate in `New()` ~line 153)
- Modify: `internal/app/stream.go` (inject catalog in `setupStreamContext` ~line 82)
- Modify: `internal/app/tool_support.go` (pass `m.skillCatalog` to `BuildRegistry` — the change deferred from Task 5)
- Test: `internal/app/mods_test.go` (add test for injection) or `internal/app/stream_test.go`

**Interfaces:**
- Consumes: `skills.Scan(cfg.SkillsDir)` and `skills.CatalogPrompt` from Tasks 2-3, `BuildRegistry(..., skillCatalog)` signature from Task 5.
- Produces: `Mods.skillCatalog` field, catalog system-prompt injection, tool registration wired through.

- [ ] **Step 1: Write the failing test for catalog injection**

First read `internal/app/mods_test.go` to find the existing `systemContents` / `TestSetupStreamContextIdentityPrompt` helper pattern (lines ~414-571). Add a test alongside:

```go
func TestSetupStreamContextInjectsSkillCatalog(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillDir), 0o700))
	require.NoError(t, os.WriteFile(skillDir, []byte("---\nname: demo\ndescription: Demo skill.\n\nbody.\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)

	cfg := DefaultTestConfig()
	cfg.SkillsDir = root
	m := newTestMods(t, cfg)
	m.skillCatalog = catalog
	require.NoError(t, m.setupStreamContext("hello", Model{}))
	contents := systemContents(m.messages)
	require.Contains(t, contents, "## Available skills")
	require.Contains(t, contents, "demo: Demo skill.")
}

func TestSetupStreamContextNoCatalogWhenEmpty(t *testing.T) {
	cfg := DefaultTestConfig()
	m := newTestMods(t, cfg)
	require.NoError(t, m.setupStreamContext("hello", Model{}))
	contents := systemContents(m.messages)
	require.NotContains(t, contents, "Available skills")
}

func TestSetupStreamContextNoCatalogInMinimalMode(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillDir), 0o700))
	require.NoError(t, os.WriteFile(skillDir, []byte("---\nname: demo\ndescription: Demo.\n\nbody.\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)

	cfg := DefaultTestConfig()
	cfg.Minimal = true
	cfg.SkillsDir = root
	m := newTestMods(t, cfg)
	m.skillCatalog = catalog
	require.NoError(t, m.setupStreamContext("hello", Model{}))
	contents := systemContents(m.messages)
	require.NotContains(t, contents, "Available skills")
}
```

**Note:** `DefaultTestConfig()` and `newTestMods(t, cfg)` are helper functions used by existing tests — find them in `mods_test.go` and reuse. If they don't exist, follow the pattern of `TestSetupStreamContextIdentityPrompt` (line 500) for constructing a `*Mods` in-test. Add `"path/filepath"`, `"os"`, and `"github.com/panjie/mods/internal/skills"` to imports as needed.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestSetupStreamContext -count=1 -v`
Expected: FAIL — `m.skillCatalog` undefined.

- [ ] **Step 3: Add the `skillCatalog` field to `Mods`**

In `internal/app/mods.go`, find the `Mods` struct fields (~line 120, after `feedbackMode`). Add:

```go
	// skillCatalog is the result of skills.Scan(cfg.SkillsDir) at New()
	// time. Empty when the skills directory is absent or has no skills;
	// in that case no catalog is injected and load_skill is not
	// registered. See docs/superpowers/specs/2026-07-06-skills-system-design.md.
	skillCatalog []skills.Skill
```

Add the import `"github.com/panjie/mods/internal/skills"` to `internal/app/mods.go`.

- [ ] **Step 4: Populate `skillCatalog` in `New()`**

In `internal/app/mods.go` `New()` (~line 143), the `return &Mods{...}` literal. Add `skillCatalog` population before the return — but since `New()` returns a literal, compute the catalog first. Insert before the `return &Mods{`:

```go
	skillCatalog, scanErr := skills.Scan(cfg.SkillsDir)
	if scanErr != nil {
		debug.Printf("skills: scan %q failed: %v (proceeding with empty catalog)", cfg.SkillsDir, scanErr)
	}
```

Then add to the `&Mods{...}` literal:

```go
		skillCatalog:        skillCatalog,
```

Add `"github.com/panjie/mods/internal/debug"` import if not already present in `mods.go`.

- [ ] **Step 5: Inject the catalog in `setupStreamContext`**

In `internal/app/stream.go` `setupStreamContext()`, find the `!cfg.Minimal` block (~line 53-83). After the project-instructions injection (~line 82), and before the `if cfg.Format != ""` block (~line 84), add:

```go
		if len(m.skillCatalog) > 0 {
			m.messages = append(m.messages, proto.Message{
				Role:    proto.RoleSystem,
				Content: skills.CatalogPrompt(m.skillCatalog),
			})
		}
```

Add the import `"github.com/panjie/mods/internal/skills"` to `stream.go` if not already present.

- [ ] **Step 6: Wire `m.skillCatalog` into `BuildRegistry` call**

In `internal/app/tool_support.go` line 48, change:

```go
		return BuildRegistry(ctx, cfg, wscfg, prompt)
```

to:

```go
		return BuildRegistry(ctx, cfg, wscfg, prompt, m.skillCatalog)
```

- [ ] **Step 7: Run the app tests**

Run: `go test ./internal/app -run TestSetupStreamContext -count=1 -v`
Expected: PASS (all three new subtests).

- [ ] **Step 8: Build the whole project**

Run: `go build ./...`
Expected: PASS (no errors — `m.skillCatalog` now exists, `BuildRegistry` signature matches).

- [ ] **Step 9: Run the full test suite**

Run: `go test ./... -count=1`
Expected: PASS (no regressions).

- [ ] **Step 10: Commit**

```bash
git add internal/app/mods.go internal/app/stream.go internal/app/tool_support.go internal/app/mods_test.go
git commit -m "feat(app): populate skillCatalog in New and inject into system prompt"
```

---

### Task 7: Identity prompt + `TestIdentityCoversConfigKeys` coverage

**Files:**
- Modify: `internal/prompts/identity.md` (add `## Skills` section; mention `skills-dir` in the config-keys list)
- Test: `internal/prompts/prompts_test.go` (add `skills-dir` to `TestIdentityCoversConfigKeys`)

**Interfaces:**
- Consumes: nothing new.
- Produces: identity.md documents the skills system so the LLM knows to call `load_skill`; the test enforces coverage.

- [ ] **Step 1: Update the test to require `skills-dir` coverage**

In `internal/prompts/prompts_test.go` `TestIdentityCoversConfigKeys` (~line 101), add `"skills-dir"` to the slice:

```go
	for _, key := range []string{
		"default-api",
		"default-model",
		"format-text",
		"roles",
		"prompts",
		"builtin-tools",
		"mcp-servers",
		"mcp-timeout",
		"apis",
		"review-mode",
		"shell-classify-prompt",
		"skills-dir",
		"~/.config/mods/mods.yml",
	} {
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/prompts -run TestIdentityCoversConfigKeys -count=1 -v`
Expected: FAIL — identity.md does not contain `skills-dir`.

- [ ] **Step 3: Add the `## Skills` section to identity.md**

In `internal/prompts/identity.md`, find a good insertion point — after the `## Config file` section (which lists config-file-only keys) or near the `## Native filesystem tools` section. Add:

```markdown
## Skills

When the user's request matches an available skill's description, call the
`load_skill` tool with that skill's name to load its full instructions, then
follow them. Skills live in `~/.config/mods/skills/<name>/SKILL.md`. Loaded
skill content stays in the conversation; do not reload the same skill twice.

Some skills reference auxiliary files in `scripts/` or `reference/`
subdirectories (e.g. "see reference/foo.md"). When a skill's body tells you
to consult such a file, call `load_skill` again with the same `name` and a
`file` parameter set to the relative path (e.g.
`load_skill("mcp-builder", "reference/mcp_best_practices.md")`). Fetch only
the files you actually need.

Skip skills when their description does not match the request.
```

Also, in the existing `## Config file` section, find the line listing config-file-only keys (it currently lists `apis`, `roles`, `prompts`, `mcp-servers`, `mcp-timeout`, `builtin-tools`, `format-text`, `shell-classify-prompt`, `max-input-chars`). Append `skills-dir` to that list so the LLM mentions it when inspecting config:

```
Config-file-only keys (no CLI flag): `apis`, `roles`, `prompts`, `mcp-servers`,
`mcp-timeout`, `builtin-tools`, `format-text`, `shell-classify-prompt`, `max-input-chars`, `skills-dir`.
```

- [ ] **Step 4: Run the prompts tests**

Run: `go test ./internal/prompts -count=1 -v`
Expected: PASS (`TestIdentityCoversConfigKeys` now finds `skills-dir`).

- [ ] **Step 5: Run the full test suite + build**

Run: `go test ./... -count=1` && `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/identity.md internal/prompts/prompts_test.go
git commit -m "docs(prompts): document skills system and skills-dir in identity.md"
```

---

### Task 8: End-to-end manual verification

**Files:** none (verification only)

- [ ] **Step 1: Build mods**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 build`
Expected: `bin/mods` written, no errors.

- [ ] **Step 2: Create a test skill**

```bash
mkdir -p ~/.config/mods/skills/demo
cat > ~/.config/mods/skills/demo/SKILL.md <<'EOF'
---
name: demo
description: A demo skill that uppercases text. Use when the user asks to uppercase something.
---

When the user asks to uppercase text:
1. Take the input they gave.
2. Respond with the uppercased version.
3. Mention that you used the "demo" skill.
EOF
```

- [ ] **Step 3: Verify the skill appears in `--list-tools`**

Run: `./bin/mods --list-tools 2>&1 | grep load_skill`
Expected: `load_skill` appears in the output.

- [ ] **Step 4: Verify the LLM loads the skill**

Run: `./bin/mods "uppercase the word hello"`
Expected: The LLM calls `load_skill("demo")`, then responds with "HELLO" and mentions it used the demo skill.

- [ ] **Step 5: Verify awesome-claude-skills compatibility**

```bash
cd /tmp/opencode && rm -rf awesome-claude-skills && git clone --depth 1 https://github.com/ComposioHQ/awesome-claude-skills
cp -r /tmp/opencode/awesome-claude-skills/changelog-generator ~/.config/mods/skills/
rm -rf ~/.config/mods/skills/demo
./bin/mods "generate a changelog from the last 3 git commits in /home/panjie/dev/mods"
```
Expected: The LLM calls `load_skill("changelog-generator")` and produces a changelog. If the skill body references auxiliary files, the LLM calls `load_skill("changelog-generator", "<path>")` to fetch them.

- [ ] **Step 6: Clean up test skills**

```bash
rm -rf ~/.config/mods/skills/changelog-generator ~/.config/mods/skills/demo
```

- [ ] **Step 7: Final full CI mirror**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 ci`
Expected: PASS (matches CI: `go build -v ./...` then `go test -v -cover -timeout=30s ./...`).

---

## Self-Review

**1. Spec coverage:**

| Spec section | Task(s) |
|---|---|
| Goal / User Experience | Tasks 1-7 (full stack) |
| Architecture (Scan → catalog → inject → tool) | Tasks 2, 3, 6 (scan), 4 (tool), 6 (inject) |
| `internal/skills/` package (Skill, Scan, CatalogPrompt) | Tasks 2, 3 |
| Frontmatter parser (hand-rolled, 2 fields, unknown ignored) | Task 2 |
| `internal/tools/skill.go` (RegisterSkill, load_skill, body + file) | Task 4 |
| Path safety for `file` (Clean, no `..`, prefix check, size cap) | Task 4 |
| Integration: mods.go field, stream.go injection, tool_support.go wiring | Task 6 |
| BuildRegistry signature + BuiltinSpecs | Task 5 |
| Config (SkillsDir, defaultSkillsDir, applySkillsDirDefault, Help, template) | Task 1 |
| Portable mode default | Task 1 (defaultSkillsDir mirrors defaultSessionDir) |
| Identity prompt update | Task 7 |
| Failure handling (all rows) | Tasks 2, 4 (parse failures, not-found, path escape, size cap) |
| Testing (all sections) | Tasks 1-7 |
| awesome-claude-skills compatibility (multi-file, unknown fields ignored) | Tasks 2 (parser), 4 (file param), 8 (e2e) |
| Non-goals (no bundled, no project-local, no recursive, no bindings) | Honored by omission |

No gaps.

**2. Placeholder scan:** Searched for "TBD", "TODO", "implement later", "add appropriate", "similar to Task". None found. All code blocks contain real Go. All commands have expected output.

**3. Type consistency:**
- `skills.Skill{Name, Description, Body, Dir string}` — used consistently in Tasks 2, 3, 4, 6.
- `skills.Scan(dir string) ([]Skill, error)` — Task 2 defines, Tasks 4, 5, 6 call.
- `skills.CatalogPrompt(skills []Skill) string` — Task 3 defines, Task 6 calls.
- `tools.RegisterSkill(reg *Registry, skills []skills.Skill) error` — Task 4 defines, Task 5 calls.
- `tooling.BuildRegistry(ctx, cfg, wscfg, prompt, skillCatalog []skills.Skill)` — Task 5 defines, Task 6 calls.
- `tools.SkillFileMaxBytes` constant — Task 4 defines, Task 4 tests reference.
- `m.skillCatalog []skills.Skill` — Task 6 defines, Tasks 5/6 reference.

All consistent.

## Execution Handoff

**Plan complete and saved to `docs/superpowers/plans/2026-07-06-skills-system.md`. Two execution options:**

**1. Subagent-Driven (recommended)** - I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** - Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
