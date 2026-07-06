# Skills System — Design

**Date:** 2026-07-06
**Status:** Approved (brainstorming complete)
**Scope:** Add a built-in skills system to mods that lets the LLM autonomously load user-defined skill instructions based on the user's request.

## Goal

When a user asks mods to do something, mods should be able to load a relevant "skill" — a markdown file of instructions/knowledge written by the user — and inject its content into the conversation so the LLM follows it. The LLM decides which skill to load by reading a compact catalog of available skills injected into the system prompt.

## Non-Goals

- No bundled skills ship with mods. Users author or install their own.
- No project-local skills (no `.mods/skills/`). Only the user-level directory.
- No tool/permission/MCP bindings declared by skills. A skill is pure prompt content. The `requires:` frontmatter field some skills declare (e.g. awesome-claude-skills' composio skills require `mcp: [rube]`) is parsed-but-ignored — mods does not gate skill loading on MCP availability. The LLM discovers a missing MCP server naturally when it tries to use it.
- No automatic discovery via keyword/embedding. The LLM is the matcher.
- No CLI flag for skills (`--list-skills`, etc. can be added later if needed).
- No recursive catalog scanning. Only top-level `*/SKILL.md` is scanned. Users copy individual skill directories into `~/.config/mods/skills/` (the awesome-claude-skills usage pattern).
- No catalog truncation. The user curates which skills are present; if they copy too many, that's their call.

## User Experience

A user copies a skill directory (e.g. from [awesome-claude-skills](https://github.com/ComposioHQ/awesome-claude-skills)) into `~/.config/mods/skills/`:

```
~/.config/mods/skills/
  git-conflict-resolver/
    SKILL.md
  mcp-builder/
    SKILL.md
    scripts/
      connections.py
      evaluation.py
    reference/
      mcp_best_practices.md
      python_mcp_server.md
```

`SKILL.md` has YAML frontmatter (`name`, `description`) and a markdown body:

```markdown
---
name: mcp-builder
description: Guide for creating high-quality MCP servers that enable LLMs to interact with external services.
license: Complete terms in LICENSE.txt
---

# MCP Server Development Guide
... see reference/mcp_best_practices.md for details ...
```

When the user asks mods to "build an MCP server for the GitHub API", the LLM sees `mcp-builder` in the system-prompt catalog, calls `load_skill("mcp-builder")` to get the body, then calls `load_skill("mcp-builder", "reference/mcp_best_practices.md")` to fetch the referenced detail. Both results stay in the conversation as tool results; the LLM follows the instructions for the rest of the session.

Unknown frontmatter fields (`license`, `requires`, etc.) are ignored by the parser — they don't break loading and don't impose any tool/MCP gating. This makes mods compatible with the broader Claude Skills ecosystem (Anthropic's open standard, awesome-claude-skills, obra/superpowers, etc.) without mods having to understand every field.

## Architecture

```
~/.config/mods/skills/<name>/SKILL.md  (+ optional scripts/, reference/, etc.)
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
  LLM reads catalog → calls load_skill("name") and optionally load_skill("name", "reference/foo.md")
            │
            ▼
  Tool returns body / file content from in-memory catalog + on-disk read
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
    Body        string // markdown body after frontmatter (content of SKILL.md)
    Dir         string // absolute path of the skill directory (for auxiliary file reads)
}

// Scan walks dir for */SKILL.md (one level, non-recursive) and returns
// skills sorted by Name. Parse failures are skipped with a debug warning;
// other skills continue. Returns nil, nil if dir does not exist or
// contains no SKILL.md.
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
| Description | `Load a skill's full instructions by name, or fetch an auxiliary file from a skill's directory. Available skills are listed in the system prompt under "Available skills".` |
| Input schema | `{ "type": "object", "required": ["name"], "properties": { "name": { "type": "string", "description": "Skill name as listed in the system prompt." }, "file": { "type": "string", "description": "Optional. Relative path to an auxiliary file inside the skill directory (e.g. 'reference/foo.md', 'scripts/run.py'). Omit to load the skill's SKILL.md body." } } }` |
| Output | The skill's `Body` text (when `file` omitted), or the file's text content (when `file` provided) |
| `Capabilities.ReadOnly` | `true` |
| `TimeoutPolicy` | `TimeoutPolicyCaller` |
| `Kind` | `ToolKindBuiltin` |

**Behavior:**

- The catalog passed to `RegisterSkill` is the in-memory `[]Skill` produced by `Scan`, which holds each skill's `Name`, `Dir`, and parsed `Body`. The tool builds a `map[string]*Skill` (name → skill pointer) at registration time.
- **`file` omitted (load SKILL.md body):** `load_skill(name)` performs a map lookup. On hit, returns the cached `Body` from the in-memory catalog — no disk access. On miss (including empty catalog), returns the error string: `skill not found: <name>. Available: <comma-separated list>` (the list helps the LLM self-correct).
- **`file` provided (load auxiliary file):** `load_skill(name, file)` looks up the skill by name (same not-found error if missing). On hit, resolves the path `filepath.Join(skill.Dir, file)` and reads it from disk. This is on-demand because auxiliary files can be large and most skills never need them.
- **Path safety for `file`:** the `file` input is validated before any disk access:
  1. Must be non-empty.
  2. Must not be absolute (no leading `/` or drive letter on Windows).
  3. `filepath.Clean(file)` must not contain `..` components (rejects `../etc/passwd`, `a/../../b`, etc.).
  4. The fully-cleaned resolved path `filepath.Join(skill.Dir, filepath.Clean(file))` must begin with `skill.Dir` + path separator. This is a defense-in-depth check; steps 2–3 already prevent escape, but step 4 catches any remaining edge case.
  5. On validation failure, return `"invalid file path: <file>"` — no disk read occurs.
- **File read:** the resolved path is read via `os.ReadFile`. A size cap (default 256 KB, see `SkillFileMaxBytes` constant) prevents accidentally loading huge files into context. If the file exceeds the cap, return `"file too large: <file> (<size> bytes, limit <limit>)"`. If the file does not exist or is not readable, return `"could not read file: <file>: <error>"`. No validation of file type/extension — the LLM can handle whatever comes back, including source code, JSON, YAML, plain text. Binary files will appear as garbage; that's acceptable (the LLM will note it and move on).
- The `name` input is treated as a map key — never used to construct a filesystem path. Empty input or input with path separators simply fails the map lookup.
- Idempotency: `load_skill(name)` always returns the same `Body` from memory (no side effects). `load_skill(name, file)` re-reads the file each call — cheap, and lets users edit auxiliary files mid-session if they want to.

The tool does not go through the `requestApproval` review flow because it is read-only and bounded to the user-configured skills directory.

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

Some skills reference auxiliary files in `scripts/` or `reference/`
subdirectories (e.g. "see reference/foo.md"). When a skill's body tells you
to consult such a file, call `load_skill` again with the same `name` and a
`file` parameter set to the relative path (e.g.
`load_skill("mcp-builder", "reference/mcp_best_practices.md")`). Fetch only
the files you actually need.

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
| `SKILL.md` frontmatter has unknown fields (`license`, `requires`, etc.) | Ignored by parser; skill loads normally |
| Frontmatter parse error (e.g. unterminated `---`) | Treat whole file as body with directory-name fallback; warn |
| Name collision (two directories resolve to same `Name`) | Later entry (by directory walk order) overwrites earlier; warn once at scan time |
| `load_skill("nonexistent")` | Map lookup misses; returns `"skill not found: nonexistent. Available: a, b, c"` |
| `load_skill("../etc/passwd")` (name is path-escape attempt) | Not a special case: `name` is a map key, never a path. Map lookup misses; returns the same not-found error as above. No filesystem access occurs. |
| `load_skill("mcp-builder", "../etc/passwd")` (file path escape) | Validation rejects before any disk read; returns `"invalid file path: ../etc/passwd"` |
| `load_skill("mcp-builder", "reference/missing.md")` | `os.ReadFile` fails; returns `"could not read file: reference/missing.md: <fs error>"` |
| `load_skill("mcp-builder", "scripts/huge.py")` where file > 256 KB | Returns `"file too large: scripts/huge.py (<size> bytes, limit 262144)"` |
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

**Body loading (`file` omitted):**
- Normal load returns body
- Nonexistent name returns error string with available list
- Path-escape inputs as `name` (`".."`, `"../etc"`, `"/etc/passwd"`, `"a/b"`) are treated as ordinary not-found lookups (no special error, no panic, no disk access)
- Empty catalog: `RegisterSkill` still registers the tool; calling it returns `"skill not found: <name>. Available: "` (empty list)
- Idempotency: two calls for the same name return identical output (body comes from the in-memory catalog)

**Auxiliary file loading (`file` provided):**
- Existing file returns content (e.g. `load_skill("mcp-builder", "reference/mcp_best_practices.md")`)
- Nonexistent file returns `"could not read file: ..."` error
- Path-escape `file` inputs rejected before disk read: `"../etc/passwd"`, `"a/../../b"`, `"/etc/passwd"`, `"*"` wildcards if they survive clean
- Absolute path rejected (Linux `/etc/passwd` and Windows-style `C:\...`)
- File exceeding size cap returns `"file too large: ..."` error
- `name` not in catalog returns the same not-found error as body loading (no file access attempted)
- Subdirectory paths work: `"reference/sub/deep.md"` resolves correctly

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

- **Bundled skills**: embed a curated set in the binary via `embed.FS`.
- **Project-local skills**: scan `.mods/skills/` in the workspace.
- **Recursive catalog scanning**: scan `**/SKILL.md` so a user can point `skills-dir` at a collection like `composio-skills/` wholesale. Currently the user copies individual skill dirs to the top level.
- **Multiple `skills-dir` paths**: support a list of directories.
- **`--list-skills` CLI flag**: print available skills and exit.
- **Catalog truncation/summarization**: cap catalog size when the user has dozens of skills. Currently the user curates the set.
- **`list_skills` tool**: on-demand browsing for large catalogs. Not needed while the user curates a small set.
- **Skill-declared tool/MCP bindings**: honoring `requires: { mcp: [...] }` frontmatter to gate skill loading on MCP availability, or `tools:` allow/deny lists. Rejected for now to keep the security model unchanged; `requires` is parsed-but-ignored.

## awesome-claude-skills Compatibility

The [awesome-claude-skills](https://github.com/ComposioHQ/awesome-claude-skills) repo (and the broader Anthropic Claude Skills open standard it follows) uses the same format this spec targets:

- `SKILL.md` with YAML frontmatter (`name`, `description`)
- Optional `scripts/` and `reference/` subdirectories for auxiliary files
- Optional frontmatter fields like `license`, `requires`

Mods is compatible with this format by construction:

1. **Frontmatter parser** ignores unknown fields, so `license`, `requires`, etc. don't break loading.
2. **`load_skill(name, file?)`** fetches both the SKILL.md body and auxiliary files in `scripts/`/`reference/`, matching the progressive-loading model the standard expects.
3. **Non-recursive scan** means the user copies individual skill directories (e.g. `cp -r awesome-claude-skills/mcp-builder ~/.config/mods/skills/`) rather than pointing at the whole repo. This avoids the 800+ skill catalog-explosion problem.
4. **`requires` field** is ignored — skills that declare MCP dependencies still load; the LLM discovers a missing MCP server when it tries to call it.

The user flow is: clone awesome-claude-skills, copy the specific skill directory wanted into `~/.config/mods/skills/`, restart mods. The skill appears in the catalog and is loadable.

## Open Questions Resolved During Brainstorming

1. *Where do skills live?* → User directory only (`~/.config/mods/skills/`).
2. *What is a skill?* → Markdown with frontmatter (`name`, `description`), body is the instruction text. Unknown frontmatter fields ignored.
3. *How does mods decide which to load?* → LLM autonomous selection from a system-prompt catalog.
4. *How is skill content delivered?* → `load_skill` tool returns the body as a tool result.
5. *Can skills bind tools/permissions?* → No, pure prompt content. `requires` field ignored.
6. *How does the LLM see the catalog?* → Static list injected into the system prompt.
7. *Frontmatter parser?* → Hand-rolled minimal parser, no YAML library.
8. *CLI flag?* → None in this iteration.
9. *Injection position?* → After the identity prompt, inside the `!cfg.Minimal` branch.
10. *awesome-claude-skills support?* → Yes. User copies individual skill dirs into `~/.config/mods/skills/`. Non-recursive scan (no catalog explosion). Multi-file skills supported via `load_skill(name, file?)` for `scripts/`/`reference/` aux files.
11. *Multi-file skills?* → In scope. `load_skill` takes optional `file` parameter; path-escape rejected via `filepath.Clean` + prefix check; 256 KB size cap.
