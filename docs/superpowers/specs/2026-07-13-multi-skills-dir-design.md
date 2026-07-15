# Multi Skills Directory Design

## Goal

Allow users to load skills from multiple directories through a single `skills-dirs` list setting. The old single-directory `skills-dir` setting is removed from the target design.

## Configuration

Use one multi-directory setting:

```yaml
skills-dirs:
  - ~/.agents/skills
  - ./project-skills
```

Rules:

- `skills-dir` is removed from config, CLI, environment, docs, and identity prompt references.
- `skills-dirs` defaults to `[~/.agents/skills]`, or `[<mods executable directory>/skills]` in portable mode.
- If two directories define the same skill name, the later directory wins.
- Empty entries are ignored.
- `~` expansion follows the existing `NormalizeSkillsDir` behavior.

## CLI and Environment

- Remove `--skills-dir`.
- Add `--skills-dirs` as a repeatable string-array flag:

```bash
mods --skills-dirs ~/.agents/skills --skills-dirs ./project-skills
```

- Remove `MODS_SKILLS_DIR`.
- Add `MODS_SKILLS_DIRS` as an environment override using the OS path-list separator:
  - Windows: `;`
  - Unix: `:`

## Internal API

Extend config with:

```go
SkillsDirs []string `yaml:"skills-dirs"`
```

Parse `MODS_SKILLS_DIRS` explicitly with `filepath.SplitList` during config loading so it uses OS path-list separators instead of the comma separator used by the generic env parser for slices.

Add:

```go
func (c Config) ResolveSkillsDirs() []string
```

This method returns the effective ordered directory list after applying the default, normalization, empty-entry filtering, and duplicate-path handling. Duplicate paths keep their last occurrence so the override rule remains consistent.

## Skill Scanning

Keep the existing `skills.Scan(dir string)` for single-directory callers and tests.

Add:

```go
func ScanDirs(dirs []string) ([]Skill, error)
```

Behavior:

- Calls `Scan` for each directory in order.
- Missing directories are ignored by the existing `Scan` behavior.
- A directory scan error is returned because it indicates an unreadable configured directory.
- Skills with the same `Name` are overwritten by later directories.
- The final catalog is sorted by `Name`, preserving stable prompt output.
- The winning skill's `Dir` is preserved so `load_skill(name, file)` reads auxiliary files from the winning directory.

## Call Sites

Replace single-directory scan/list behavior with effective directories:

- App startup uses `skills.ScanDirs(cfg.ResolveSkillsDirs())`.
- `mods --list-skills` lists the merged catalog from the effective directory list.
- Tooling and tests that only need one directory may keep using `skills.Scan` directly.

## Documentation and Prompt Updates

Update the following so self-help tests remain accurate:

- `internal/config/config_template.yml`
- `internal/config/config.go` `Help` map
- `internal/prompts/identity.md`
- prompt/config coverage tests for the new `skills-dirs` key

## Testing

Add tests for:

- Default effective dirs are `[~/.agents/skills]`, or `[<mods executable directory>/skills]` in portable mode.
- `skills-dir`, `--skills-dir`, and `MODS_SKILLS_DIR` are no longer recognized as supported configuration surfaces.
- `skills-dirs` YAML supports multiple entries.
- `MODS_SKILLS_DIRS` parses OS path-list separators.
- `ResolveSkillsDirs` expands `~`, removes empty entries, and keeps duplicate paths only at the last occurrence.
- `ScanDirs` merges multiple directories and lets later directories override earlier same-name skills.
- `load_skill` auxiliary file reads from the overriding skill directory.
- `mods --list-skills` uses the merged catalog.
