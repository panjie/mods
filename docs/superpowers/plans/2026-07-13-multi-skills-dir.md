# Multi Skills Directory Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace single-directory skills configuration with `skills-dirs`, an ordered list of skill directories.

**Architecture:** Remove the old `skills-dir` config field, flag, and env var from the target implementation. Store only `SkillsDirs []string`, resolve it to a normalized ordered list with a default of `~/.agents/skills` (or `<mods executable directory>/skills` in portable mode), and scan all effective directories with later same-name skills overriding earlier ones.

**Tech Stack:** Go, Cobra/pflag `StringArray`, YAML config, explicit environment parsing with `filepath.SplitList`, existing `internal/config`, `internal/skills`, `internal/app`, and `internal/cli` packages.

## Global Constraints

- `skills-dir`, `--skills-dir`, and `MODS_SKILLS_DIR` are removed from the target design.
- `skills-dirs` is the only supported skills directory configuration key.
- `skills-dirs` defaults to `[~/.agents/skills]` when unset or empty, or `[<mods executable directory>/skills]` in portable mode.
- `--skills-dirs` is repeatable; order is preserved.
- `MODS_SKILLS_DIRS` uses `filepath.SplitList`: Windows `;`, Unix `:`.
- Empty directory entries are ignored.
- `~` expansion follows existing `NormalizeSkillsDir` behavior.
- Duplicate paths keep their last occurrence.
- Later directories override earlier same-name skills.
- Final skill catalog output remains sorted by skill name.
- The winning skill's `Dir` must be preserved for `load_skill(name, file)` auxiliary reads.
- Update config template, help, identity docs, and coverage tests for `skills-dirs`; remove `skills-dir` coverage.

---

### Task 1: Config Uses `skills-dirs` Only

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Produces: `PersistentConfig.SkillsDirs []string`, `Config.ResolveSkillsDirs() []string`, `parseSkillsDirsEnv(*Config) error`.
- Removes: `PersistentConfig.SkillsDir string`, `applySkillsDirDefault(*Config)` usage as a single-directory field normalizer.
- Preserves: `NormalizeSkillsDir(path string) string`, `defaultSkillsDir() string`.

- [ ] **Step 1: Write failing config tests**

Replace existing `skills-dir` config tests in `internal/config/config_test.go` with:

```go
func TestResolveSkillsDirsDefault(t *testing.T) {
	cfg := Config{}
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsYAMLOverride(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("skills-dirs:\n  - ~/team-skills\n  - ./project-skills\n"), &cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{
		filepath.Join(xdg.Home, "team-skills"),
		filepath.Join(".", "project-skills"),
	}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsEnvPathList(t *testing.T) {
	t.Setenv("MODS_SKILLS_DIRS", filepath.Join("env", "one")+string(os.PathListSeparator)+filepath.Join("env", "two"))
	cfg := Default()
	require.NoError(t, parseSkillsDirsEnv(&cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join("env", "one"), filepath.Join("env", "two")}, cfg.ResolveSkillsDirs())
}

func TestResolveSkillsDirsIgnoresEmptyAndKeepsLastDuplicate(t *testing.T) {
	cfg := Config{PersistentConfig: PersistentConfig{
		SkillsDirs: []string{"/skills/global", "", "/skills/project", "/skills/global"},
	}}
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{"/skills/project", "/skills/global"}, cfg.ResolveSkillsDirs())
}

func TestSkillsDirLegacyKeyIsIgnored(t *testing.T) {
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte("skills-dir: /legacy\n"), &cfg))
	applySkillsDirsDefault(&cfg)
	require.Equal(t, []string{filepath.Join(xdg.Home, ".agents", "skills")}, cfg.ResolveSkillsDirs())
}
```

- [ ] **Step 2: Run config tests and verify RED**

Run: `go test ./internal/config -run "TestResolveSkillsDirs|TestSkillsDirLegacyKeyIsIgnored" -count=1`

Expected: FAIL to compile because `SkillsDirs`, `applySkillsDirsDefault`, `ResolveSkillsDirs`, and `parseSkillsDirsEnv` do not exist, and old `SkillsDir` code still exists.

- [ ] **Step 3: Implement `SkillsDirs` config**

In `internal/config/config.go`, replace the old field:

```go
SkillsDir string `yaml:"skills-dir" env:"SKILLS_DIR"`
```

with:

```go
SkillsDirs []string `yaml:"skills-dirs"`
```

Add helpers near existing skills-dir helpers:

```go
func parseSkillsDirsEnv(c *Config) error {
	value := os.Getenv("MODS_SKILLS_DIRS")
	if value == "" {
		return nil
	}
	c.SkillsDirs = filepath.SplitList(value)
	return nil
}

func applySkillsDirsDefault(c *Config) {
	if len(c.ResolveSkillsDirs()) == 0 {
		c.SkillsDirs = []string{defaultSkillsDir()}
		return
	}
	c.SkillsDirs = c.ResolveSkillsDirs()
}

func (c Config) ResolveSkillsDirs() []string {
	raw := make([]string, 0, len(c.SkillsDirs))
	raw = append(raw, c.SkillsDirs...)

	last := make(map[string]int, len(raw))
	normalized := make([]string, 0, len(raw))
	for _, dir := range raw {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		dir = NormalizeSkillsDir(dir)
		normalized = append(normalized, dir)
		last[dir] = len(normalized) - 1
	}

	result := normalized[:0]
	for i, dir := range normalized {
		if last[dir] == i {
			result = append(result, dir)
		}
	}
	return result
}
```

In `Ensure`, replace `applySkillsDirDefault` calls with `applySkillsDirsDefault`. After `env.ParseWithOptions(&c, env.Options{Prefix: "MODS_"})`, call:

```go
if err := parseSkillsDirsEnv(&c); err != nil {
	return fallback, modsError{Err: err, ReasonText: "Could not parse environment into settings file."}
}
```

- [ ] **Step 4: Run config tests and verify GREEN**

Run: `go test ./internal/config -run "TestResolveSkillsDirs|TestSkillsDirLegacyKeyIsIgnored|TestSkillsDir" -count=1`

Expected: PASS after replacing/removing old `TestSkillsDir...` cases or updating them to `skills-dirs`.

---

### Task 2: Multi-Directory Skill Scanning

**Files:**
- Modify: `internal/skills/skill.go`
- Modify: `internal/skills/scan_test.go`
- Modify: `internal/tools/skill_test.go`

**Interfaces:**
- Consumes: `Scan(dir string) ([]Skill, error)`.
- Produces: `ScanDirs(dirs []string) ([]Skill, error)`.

- [ ] **Step 1: Write failing scanner tests**

Add to `internal/skills/scan_test.go`:

```go
func TestScanDirsMergesAndLaterDirsOverride(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeSkill(t, first, "shared", "---\nname: shared\ndescription: Shared from first.\n---\n\nfirst body\n")
	writeSkill(t, first, "alpha", "---\nname: alpha\ndescription: Alpha skill.\n---\n\nalpha body\n")
	writeSkill(t, second, "shared", "---\nname: shared\ndescription: Shared from second.\n---\n\nsecond body\n")
	writeSkill(t, second, "beta", "---\nname: beta\ndescription: Beta skill.\n---\n\nbeta body\n")

	got, err := ScanDirs([]string{first, second})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"alpha", "beta", "shared"}, []string{got[0].Name, got[1].Name, got[2].Name})
	require.Equal(t, "Shared from second.", got[2].Description)
	require.Equal(t, filepath.Join(second, "shared"), got[2].Dir)
}

func TestScanDirsMissingDirsAreIgnored(t *testing.T) {
	got, err := ScanDirs([]string{filepath.Join(t.TempDir(), "missing")})
	require.NoError(t, err)
	require.Nil(t, got)
}
```

Add to `internal/tools/skill_test.go`:

```go
func TestLoadSkillAuxFileUsesOverridingSkillDir(t *testing.T) {
	first, catalog1 := setupSkillFixture(t)
	second := t.TempDir()
	multiDir := filepath.Join(second, "multi")
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "reference"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "SKILL.md"), []byte("---\nname: multi\ndescription: Override multi.\n---\n\nOverride instructions.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "reference", "detail.md"), []byte("Override detail.\n"), 0o600))

	catalog, err := skills.ScanDirs([]string{first, second})
	require.NoError(t, err)
	require.NotEmpty(t, catalog1)

	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/detail.md"}`)
	require.Equal(t, "Override detail.\n", got)
}
```

- [ ] **Step 2: Run scanner tests and verify RED**

Run: `go test ./internal/skills ./internal/tools -run "TestScanDirs|TestLoadSkillAuxFileUsesOverridingSkillDir" -count=1`

Expected: FAIL to compile because `ScanDirs` does not exist.

- [ ] **Step 3: Implement `ScanDirs`**

Add to `internal/skills/skill.go`:

```go
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
```

- [ ] **Step 4: Run scanner tests and verify GREEN**

Run: `go test ./internal/skills ./internal/tools -run "TestScanDirs|TestLoadSkillAuxFileUsesOverridingSkillDir|TestLoadSkillAuxFilePathEscapeRejected" -count=1`

Expected: PASS.

---

### Task 3: App and CLI Use `skills-dirs`

**Files:**
- Modify: `internal/app/mods.go`
- Modify: `internal/cli/main.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/list_skills.go`
- Modify: `internal/cli/main_test.go`
- Modify: `internal/cli/list_skills_test.go`
- Modify: `internal/tooling/tools_test.go`

**Interfaces:**
- Consumes: `Config.ResolveSkillsDirs() []string`, `skills.ScanDirs([]string)`.
- Produces: repeatable `--skills-dirs` flag and merged `mods --list-skills` output.

- [ ] **Step 1: Write failing CLI tests**

Add to `internal/cli/main_test.go`:

```go
func TestSkillsDirsFlagAppendsMultipleDirs(t *testing.T) {
	withTestConfig(t, Config{}, func() {
		require.Nil(t, rootCmd.Flags().Lookup("skills-dir"))
		require.NotNil(t, rootCmd.Flags().Lookup("skills-dirs"))
		require.NoError(t, rootCmd.Flags().Set("skills-dirs", "/one"))
		require.NoError(t, rootCmd.Flags().Set("skills-dirs", "/two"))
		require.Equal(t, []string{"/one", "/two"}, config.SkillsDirs)
	})
}
```

Update `internal/cli/list_skills_test.go` so `listSkills` accepts `[]string`, then add:

```go
func TestListSkillsUsesMergedDirectories(t *testing.T) {
	withListSkillsOutputTest(t, false, false)
	first := t.TempDir()
	second := t.TempDir()
	writeCLITestSkill(t, first, "shared", "Shared first.")
	writeCLITestSkill(t, second, "shared", "Shared second.")
	writeCLITestSkill(t, second, "project", "Project skill.")

	output := captureStdout(t, func() {
		require.NoError(t, listSkills(nil, []string{first, second}))
	})

	require.Contains(t, output, "Directories:")
	require.Contains(t, output, "`"+first+"`")
	require.Contains(t, output, "`"+second+"`")
	require.Contains(t, output, "- **project** — Project skill.")
	require.Contains(t, output, "- **shared** — Shared second.")
}
```

- [ ] **Step 2: Run CLI tests and verify RED**

Run: `go test ./internal/cli -run "TestSkillsDirsFlagAppendsMultipleDirs|TestListSkillsUsesMergedDirectories" -count=1`

Expected: FAIL because `--skills-dirs`, the new list signature, and merged scanner seam do not exist yet.

- [ ] **Step 3: Update app startup**

In `internal/app/mods.go`, replace:

```go
skillCatalog, scanErr := skills.Scan(cfg.SkillsDir)
```

with:

```go
skillDirs := cfg.ResolveSkillsDirs()
skillCatalog, scanErr := skills.ScanDirs(skillDirs)
```

Update debug text to use `%v` and `skillDirs`.

- [ ] **Step 4: Update CLI flags and list-skills**

In `internal/cli/main.go`, remove the `regStr(... "skills-dir" ...)` registration and add:

```go
regStrArr(flags, &config.SkillsDirs, "skills-dirs", "", config.SkillsDirs)
```

In `markCategory`, replace `"skills-dir"` with `"skills-dirs"`.

In `internal/cli/run.go`, replace `listSkills(mods, config.SkillsDir)` with:

```go
listSkills(mods, config.ResolveSkillsDirs())
```

In `internal/cli/list_skills.go`, change:

```go
var scanSkills = skills.ScanDirs

func listSkills(mods *Mods, dirs []string) error {
	catalog, err := scanSkills(dirs)
	// existing error handling
	markdown := skillsMarkdown(dirs, catalog)
	// existing render/raw handling
}
```

Change `skillsMarkdown` to accept `[]string` and render `Directory:` for one entry, `Directories:` for multiple entries.

- [ ] **Step 5: Run app/CLI tests and verify GREEN**

Run: `go test ./internal/cli ./internal/app ./internal/tooling -run "TestSkillsDirs|TestListSkills|Test.*Skill" -count=1`

Expected: PASS.

---

### Task 4: Docs, Template, Help, and Identity

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_template.yml`
- Modify: `internal/prompts/identity.md`
- Modify: `internal/prompts/prompts_test.go`

**Interfaces:**
- Consumes: existing `Help` map and prompt coverage tests.
- Produces: docs for `skills-dirs`; removes docs/coverage for `skills-dir`.

- [ ] **Step 1: Write/update failing coverage tests**

In `internal/prompts/prompts_test.go`, replace expected config key `"skills-dir"` with `"skills-dirs"`.

Run: `go test ./internal/prompts -run "TestIdentityCovers" -count=1`

Expected: FAIL until `identity.md` documents `skills-dirs` and no longer relies on `skills-dir`.

- [ ] **Step 2: Update help and template**

In `internal/config/config.go` `Help`, remove `skills-dir` and add:

```go
"skills-dirs": "Directories containing installed skills. Can be set multiple times; later directories override earlier same-name skills. Defaults to ~/.agents/skills, or a skills directory next to the executable in portable mode.",
```

In `internal/config/config_template.yml`, replace the `skills-dir` example with:

```yaml
# {{ index .Help "skills-dirs" }}
# skills-dirs:
#   - ~/.agents/skills
#   - ./project-skills
```

- [ ] **Step 3: Update identity prompt**

In `internal/prompts/identity.md`, replace `skills-dir` references with:

```markdown
- `skills-dirs`: Directories containing installed skills. Defaults to `~/.agents/skills`, or `<mods executable directory>/skills` in portable mode. Later directories override earlier same-name skills.
- `--skills-dirs <dir>`: Add a skills directory. Repeat to add multiple directories.
```

- [ ] **Step 4: Run full verification**

Run:

```bash
go test ./internal/config ./internal/prompts ./internal/skills ./internal/tools ./internal/cli ./internal/app ./internal/tooling -count=1
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```

Expected: PASS.

---

## Self-Review Checklist

- Spec coverage: removal of `skills-dir`, new `skills-dirs` config/env/CLI, scanning merge, override behavior, list-skills, load_skill auxiliary file, template, Help, identity, and tests are covered.
- Placeholder scan: no `TBD`, `TODO`, or unspecified implementation steps remain.
- Type consistency: the plan consistently uses `SkillsDirs []string`, `ResolveSkillsDirs() []string`, and `ScanDirs([]string)`.
