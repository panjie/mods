# Skills System — Design

**Date:** 2026-07-06
**Status:** Approved (brainstorming complete)
**Scope:** Add a built-in skills system to mods that lets the LLM autonomously load user-defined skill instructions based on the user's request.

## Goal

When a user asks mods to do something, mods should be able to load a relevant "skill" — a markdown file of instructions/knowledge written by the user — and inject its content into the conversation so the LLM follows it. The LLM decides which skill to load by reading a compact catalog of available skills injected into the system prompt.

## Non-Goals

- No bundled skills ship with mods. Users author or install their own.
- No project-local skills (no `.mods/skills/`). Only the user-level directory.
- No tool/permission/MCP bindings declared by skills. A skill is pure prompt content.
- No automatic discovery via keyword/embedding. The LLM is the matcher.
- No CLI flag for skills (`--list-skills`, etc. can be added later if needed).
- No multi-file skills in this iteration (a future extension can add a `file` parameter to `load_skill`).

## User Experience

A user creates `~/.config/mods/skills/git-conflict-resolver/SKILL.md`:

```markdown
---
name: git-conflict-resolver
description: Resolves Git merge conflicts by reading conflict markers, analyzing both sides, and writing resolved files.
---

When asked to resolve merge conflicts:
1. Read the conflicted file with fs_read_file.
2. Identify the <<<<<<<, =======, >>>>>>> markers.
3. ...
```

Later, when the user asks mods "resolve the merge conflicts in main.go", the LLM sees the skill in the system-prompt catalog, calls `load_skill("git-conflict-resolver")`, receives the body of the markdown as a tool result, and follows those instructions for the rest of the conversation.

## Architecture

```
~/.config/mods/skills/<name>/SKILL.md
            │
            ▼
internal/skills.Scan()  ──► []Skill{name, description, body, dir}
            │
            ▼
  ┌───────────────────────────────────────────────┐
  │ setupStreamContext (internal/app/stream.go):  │
  │   append system msg: "Available skills: ..." │  ◄── catalog injection
  └───────────────────────────────────────────────┘
            │
            ▼
  BuildRegistry ──► register load_skill (only when catalog non-empty)
            │
            ▼
  LLM reads catalog → calls load_skill("name")
            │
            ▼
  Tool returns body from in-memory catalog (map lookup)
            │
            ▼
  Subsequent turns: tool result already in history, no reload needed
```

**Scan happens once per conversation start**, in `Mods` initialization. The result is cached on `m.skillCatalog`. When the catalog is empty, the entire mechanism is bypassed: no system-prompt section is appended and `load_skill` is not registered.

## Components

### New package `internal/skills/`

```go
// Skill is the parsed result of one skill directory.
type Skill struct {
    Name        string // frontmatter.name, or directory name fallback
    Description string // frontmatter.description, or "(no description)" fallback
    Body        string // markdown body after frontmatter
    Dir         string // absolute path of the skill directory
}

// Scan walks dir for */SKILL.md and returns skills sorted by Name.
// Parse failures are skipped with a debug warning; other skills continue.
// Returns nil, nil if dir does not exist or contains no SKILL.md.
func Scan(dir string) ([]Skill, error)

// CatalogPrompt renders the system-prompt section listing available skills.
// Returns "" for an empty slice (caller skips injection entirely).
func CatalogPrompt(skills []Skill) string
```

**Frontmatter parsing:** minimal hand-rolled parser, no YAML dependency. The schema is exactly two fields (`name`, `description`). Algorithm:

1. If the file does not start with `---\n`, treat the whole file as body and use the directory name as `Name`.
2. Otherwise, find the next `\n---\n` (or `\n---` at EOF) as the frontmatter terminator. Between the markers, scan line by line for `name:` and `description:` prefixes (case-insensitive, whitespace-trimmed). Anything else in the block is ignored (forward-compat: extra fields don't break parsing).
3. The remainder after the closing `---` is the `Body`, with one leading newline trimmed.

This avoids adding `gopkg.in/yaml.v3` to the dependency tree for two fields.

### New tool `internal/tools/skill.go`

```go
// RegisterSkill registers the load_skill tool. skills is the scan result
// the tool will serve at runtime; an empty slice is allowed and produces
// a tool whose Call closure always returns "no skills available".
// RegisterSkill is unconditional — the *caller* decides whether to register
// at all (BuildRegistry skips it when the runtime catalog is empty).
func RegisterSkill(reg *Registry, skills []skills.Skill) error
```

Tool spec:

| Field | Value |
|---|---|
| Name | `load_skill` |
| Description | `Load a skill's full instructions by name. Available skills are listed in the system prompt under "Available skills".` |
| Input schema | `{ "type": "object", "required": ["name"], "properties": { "name": { "type": "string" } } }` |
| Output | The skill's `Body` text |
| `Capabilities.ReadOnly` | `true` |
| `TimeoutPolicy` | `TimeoutPolicyCaller` |
| `Kind` | `ToolKindBuiltin` |

**Behavior:**

- The catalog passed to `RegisterSkill` is the in-memory `[]Skill` produced by `Scan`, which already holds each skill's parsed `Body`. The tool builds a `map[string]string` (name → body) from this slice at registration time.
- `load_skill(name)` performs a map lookup. On hit, returns the cached `Body`. On miss (including when the catalog is empty), returns the error string: `skill not found: <name>. Available: <comma-separated list>` (the list is empty when the catalog itself is empty). The list helps the LLM self-correct.
- The `name` input is treated purely as a map key — it is never used to construct a filesystem path. This eliminates path-escape risk by construction: there is no path to escape. Empty input or input containing path separators simply fails the map lookup and returns the not-found error.
- Idempotency is automatic: the same `name` always returns the same `Body` from the same in-memory map, with no side effects and no disk access at call time.

The tool does not go through the `requestApproval` review flow because it has no side effects and touches no user data — it only returns text that was already read from a trusted directory at scan time.

### Integration points

**`internal/app/mods.go`** — Add field `skillCatalog []skills.Skill` to `Mods`. Populate during conversation init by calling `skills.Scan(cfg.SkillsDir)`. Errors from `Scan` are non-fatal: log via `debug` and treat as an empty catalog.

**`internal/app/stream.go` `setupStreamContext`** — Inside the `!cfg.Minimal` branch, after the identity prompt is appended, if `len(m.skillCatalog) > 0`, append a `proto.Message{Role: RoleSystem, Content: skills.CatalogPrompt(m.skillCatalog)}`. Minimal mode skips injection (consistent with identity.md behavior).

**`internal/tooling/tools.go` `BuildRegistry`** — Add a `skills []skills.Skill` parameter (explicit is better than hiding it in `cfg`; the catalog is computed once upstream and passed in). Update all callers. When the slice is non-empty, call `toolregistry.RegisterSkill(registry, skills)`. When empty, skip registration so the runtime tool surface stays clean.

**`internal/tooling/tools.go` `BuiltinSpecs`** — Call `RegisterSkill(registry, nil)` so the `load_skill` spec is harvested for `--list-tools`. The `Call` closure is never invoked in this path (per the existing comment on `BuiltinSpecs`), so a nil catalog is safe and the spec appears in the listing regardless of runtime configuration.

### Config

New config-file-only key (no CLI flag):

```yaml
# skills-dir: /home/you/.config/mods/skills   # absolute path; ~ is NOT expanded
```

- `internal/config/config.go`: add `SkillsDir string` field, yaml tag `skills-dir`, env `MODS_SKILLS_DIR`.
- Default value: resolved via the same pattern as `defaultSessionDir()` — `filepath.Join(xdg.ConfigHome, "mods", "skills")` in standard mode, `filepath.Join(executableDir(), "skills")` in portable mode. Human-readable description in docs/help is `~/.config/mods/skills` (Linux).
- `internal/config/config_template.yml`: add as a commented-out key (matching the `# web-search-api-key:` pattern for optional path keys):
  ```yaml
  # {{ index .Help "skills-dir" }}
  # skills-dir:
  ```
  The default is applied at `Ensure` time, not stored in the template.
- Default application: add an `applySkillsDirDefault(c *Config)` function called from `Ensure` (mirroring `applySessionDirDefault`). When `c.SkillsDir == ""`, set it to `defaultSkillsDir()` which returns `filepath.Join(executableDir(), "skills")` in portable mode or `filepath.Join(xdg.ConfigHome, "mods", "skills")` otherwise.
- `Help` map: add `"skills-dir": "Directory containing user-defined skills (one subdirectory per skill, each with a SKILL.md). Defaults to ~/.config/mods/skills (or next to the executable in portable mode)."`.
- Tilde expansion: none. Go's `filepath` functions do not expand `~`, and the existing config code does not either. Users who want a home-relative path must use `$HOME` or an absolute path. This matches the behavior of every other path-valued config key in mods.

### Identity prompt

Add a section to `internal/prompts/identity.md`:

```markdown
## Skills

When the user's request matches an available skill's description, call the
`load_skill` tool with that skill's name to load its full instructions, then
follow them. Skills live in `~/.config/mods/skills/<name>/SKILL.md`. Loaded
skill content stays in the conversation; do not reload the same skill twice.
Skip skills when their description does not match the request.
```

The `TestIdentityCoversAllFlags` and `TestIdentityCoversConfigKeys` tests in `internal/prompts/prompts_test.go` must cover the new `skills-dir` config key.

## Catalog Format

`CatalogPrompt` renders:

```
## Available skills

Call load_skill(<name>) to load a skill's full instructions.
- <name>: <description>
- <name>: <description>
```

One line per skill, sorted alphabetically by `Name` (the `Scan` contract). No truncation in this iteration; if a user has so many skills that the catalog dominates the system prompt, that is a self-imposed problem solvable by removing skills. A future iteration can add truncation if real usage demands it.

## Failure Handling

| Situation | Behavior |
|---|---|
| `skills-dir` does not exist | `Scan` returns `nil, nil`; no injection, no tool registration |
| `skills-dir` exists but no `*/SKILL.md` | Same as above |
| `SKILL.md` missing frontmatter | Whole file treated as body; `Name` falls back to directory name; warn via `debug` |
| `SKILL.md` frontmatter missing `name` | Fall back to directory name; warn |
| `SKILL.md` frontmatter missing `description` | Use `"(no description)"` placeholder; warn |
| Frontmatter parse error (e.g. unterminated `---`) | Treat whole file as body with directory-name fallback; warn |
| Name collision (two directories resolve to same `Name`) | Later entry (by directory walk order) overwrites earlier; warn once at scan time |
| `load_skill("nonexistent")` | Map lookup misses; returns `"skill not found: nonexistent. Available: a, b, c"` |
| `load_skill("../etc/passwd")` or any path-escape attempt | Not a special case: the input is a map key, never a path. Map lookup misses; returns the same not-found error as above. No filesystem access occurs. |
| Minimal mode | No catalog injection, no tool registration (matches identity.md skipping) |

All warnings go through the existing `debug` logger so they appear only when `MODS_DEBUG` is set.

## Testing

### `internal/skills/scan_test.go`

- Normal parse: frontmatter with both fields + body
- Missing `name`: falls back to directory name
- Missing `description`: placeholder used
- No frontmatter: whole file is body, name from directory
- Unterminated frontmatter: graceful fallback
- Name collision: later overwrites earlier
- Empty/nonexistent directory: `nil, nil`, no error
- `CatalogPrompt([])` returns `""`
- `CatalogPrompt` render format correct (header + one line per skill)
- Skills sorted by name in output

### `internal/tools/skill_test.go`

- Normal load returns body
- Nonexistent name returns error string with available list
- Path-escape inputs (`".."`, `"../etc"`, `"/etc/passwd"`, `"a/b"`) are treated as ordinary not-found lookups (no special error, no panic)
- Empty catalog: `RegisterSkill` still registers the tool; calling it returns `"skill not found: <name>. Available: "` (empty list)
- Idempotency: two calls for the same name return identical output (the body comes from the same in-memory map; no disk access at call time to verify)

### `internal/app/*_test.go`

- Catalog non-empty: system messages contain the catalog section
- Catalog empty: no catalog system message; `load_skill` not in registry
- Minimal mode: no catalog injection even when catalog is non-empty

### `internal/prompts/prompts_test.go`

- `TestIdentityCoversConfigKeys` extended to assert `skills-dir` is documented in identity.md

### `internal/config/config_test.go`

- Default `skills-dir` value
- `MODS_SKILLS_DIR` env override
- YAML `skills-dir` override
- Portable-mode relative path resolution (if a portable test fixture already exists, reuse it)

## Out of Scope / Future Extensions

- **Multi-file skills**: allow `load_skill(name, file?)` to fetch additional `.md` files from the skill directory. The `file` parameter is intentionally omitted from the input schema now; adding it later is backward-compatible.
- **Bundled skills**: embed a curated set in the binary via `embed.FS`.
- **Project-local skills**: scan `.mods/skills/` in the workspace.
- **`--list-skills` CLI flag**: print available skills and exit.
- **Truncation/summarization**: cap catalog size when the user has dozens of skills.
- **Skill-declared tool bindings**: frontmatter `tools:` allow/deny lists. Rejected for now to keep the security model unchanged.

## Open Questions Resolved During Brainstorming

1. *Where do skills live?* → User directory only (`~/.config/mods/skills/`).
2. *What is a skill?* → Markdown with frontmatter (`name`, `description`), body is the instruction text.
3. *How does mods decide which to load?* → LLM autonomous selection from a system-prompt catalog.
4. *How is skill content delivered?* → `load_skill` tool returns the body as a tool result.
5. *Can skills bind tools/permissions?* → No, pure prompt content.
6. *How does the LLM see the catalog?* → Static list injected into the system prompt.
7. *Frontmatter parser?* → Hand-rolled minimal parser, no YAML library.
8. *CLI flag?* → None in this iteration.
9. *Injection position?* → After the identity prompt, inside the `!cfg.Minimal` branch.
