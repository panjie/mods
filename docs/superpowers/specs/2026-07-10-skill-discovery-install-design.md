# Skill Discovery & Installation — Design

**Date:** 2026-07-10
**Status:** Approved (brainstorming complete)
**Depends on:** [Skills System — Design](./2026-07-06-skills-system-design.md) (the existing `load_skill` / `skills.Scan` foundation)

## Goal

Let mods **search** configurable remote skill sources and **install** a skill on demand during a conversation, returning the skill body immediately so the LLM can use it in-session. The LLM discovers what it needs via a `search_skills` tool; installation goes through the existing review/approval flow so the user confirms every install.

## Non-Goals

- **No uninstall / update / upgrade tools.** Users manage the local `skills-dir` manually (delete a directory to remove; re-run install to refresh is a future extension).
- **No CLI subcommands** (`mods skills install ...`). Installation happens in-conversation via tools. A CLI can be layered on later.
- **No LLM-provided source URLs.** The LLM never supplies a git URL — it can only install from sources the user pre-configured. This is a hard security boundary.
- **No recursive scanning of source repos.** A source's `path` is scanned one level deep (`<path>/<skill>/SKILL.md`), consistent with the existing `skills.Scan` contract. Repos with deeply nested category layouts (e.g. awesome-claude-skills' category folders) are a future concern.
- **No full remote catalog injected into the system prompt.** Discovery is via the `search_skills` tool on demand, avoiding system-prompt bloat for large sources.
- **No semantic/embedding search.** Matching is case-insensitive substring on name + description, with name-hit prioritization. The LLM is the ultimate matcher (consistent with the existing skills philosophy).
- **No automatic install.** Every install requires explicit user approval via the review flow.

## User Experience

A user adds skill sources to `mods.yml` (a sensible default ships built in):

```yaml
skill-sources:
  - url: https://github.com/obra/superpowers.git
    path: skills          # subdir within the repo containing */SKILL.md; default "."
```

During a conversation, when the LLM realizes it would benefit from a skill that is not installed locally, it calls:

```
search_skills("test driven development")
```

mods shallow-clones each configured source into a local cache (once per session), scans them, and returns matches:

```
test-driven-development — Use when implementing any feature or bugfix... (source: superpowers)
verification-before-completion — Use when about to claim work is complete... (source: superpowers)
```

The LLM picks one and calls:

```
install_skill("test-driven-development")
```

The user sees a review prompt showing the source, skill name, description, and a note that the skill's instructions will be followed. On approval, mods copies the skill directory into `~/.config/mods/skills/`, parses `SKILL.md`, and **returns the body directly** — the LLM follows the instructions for the rest of the session. The skill is also present in future sessions' local catalogs.

## Architecture

```
config skill-sources [{url, path}]
        │   (lazy, on first search_skills call of the session)
        ▼
SyncSources ── git clone --depth 1 <url> → <cache>/<slug>/   (exists? git pull --ff-only; pull fails → delete + reclone)
        │
        ▼   skills.Scan(<clone>/<path>) per source
build remote catalog []SourceSkill{name, description, source, dir}  ── cached on Mods for the session
        │
        ▼
search_skills(query) ── case-insensitive substring match (name-hit first) → top N text list
        │   (read-only, no approval)
        ▼   LLM picks → install_skill(name, source?)
        │   → requestApproval → user confirms
copy <clone>/<path>/<name>/ → <skillsDir>/<name>/
        │
        ▼
parse SKILL.md → return Body (immediate use)
        (skill is now in the local skills-dir; appears in future sessions' local catalogs)
```

**Sync is lazy** (first `search_skills`/`install_skill` call in a session), not at startup, so conversations that never touch remote skills pay no network cost. The resulting `[]SourceSkill` is cached on `Mods` for the rest of the session; subsequent searches are local and instant.

## Components

### Config — `skill-sources`

New config-file-only key (no CLI flag, no env var; a list of structs like `mcp-servers`):

```yaml
skill-sources:
  - url: https://github.com/obra/superpowers.git
    path: skills
  - url: https://github.com/ComposioHQ/awesome-claude-skills.git
    path: .
```

- `internal/config/config.go`:
  - Add `SkillSource` struct: `URL string` (yaml `url`) + `Path string` (yaml `path`).
  - Add `SkillSources []SkillSource` field on `PersistentConfig`, yaml tag `skill-sources`.
  - Add `defaultSkillSources() []SkillSource` returning `[{URL: "https://github.com/obra/superpowers.git", Path: "skills"}]`.
  - Add `applySkillSourcesDefault(c *Config)` mirroring `applySkillsDirDefault`: when `c.SkillSources == nil`, set the default. Call it from `Ensure` (both code paths, next to `applySkillsDirDefault`).
  - Normalize each source on load: empty `Path` → `"."`; trim trailing slashes on `URL`.
- `Help` map: add `"skill-sources": "List of remote git repositories mods can search and install skills from. Each entry has a git 'url' and an optional 'path' (subdir containing skill directories; defaults to the repo root)."`.
- `internal/config/config_template.yml`: add as a commented-out key:
  ```yaml
  # {{ index .Help "skill-sources" }}
  # skill-sources:
  #   - url: https://github.com/obra/superpowers.git
  #     path: skills
  ```
- Tilde expansion: none (consistent with other path/URL config keys).
- Default application happens at `Ensure` time, not stored in the template.

### Cache directory (source clones)

The source-clone cache is **derived, not user-configurable** (avoids config bloat). Add to `internal/config/config.go`:

```go
// defaultSkillSourcesCacheDir resolves the directory where remote skill source
// repos are shallow-cloned. Regenerable, so it lives under the cache home.
// Portable mode: next to the executable (self-contained), mirroring the
// skills/sessions defaults.
func defaultSkillSourcesCacheDir() string {
    if portableActive() {
        return filepath.Join(executableDir(), "skill-sources")
    }
    return filepath.Join(xdg.CacheHome, "mods", "skill-sources")
}
```

Resolved examples:
- Linux (this machine, `XDG_CACHE_HOME=/home/panjie/.cache`): `/home/panjie/.cache/mods/skill-sources/`
- Windows (`XDG_CACHE_HOME` unset): `C:\Users\<user>\AppData\Local\cache\mods\skill-sources\` (adrg/xdg's `CacheHome` defaults to `%LOCALAPPDATA%\cache`)
- Portable mode (any OS): `<executable-dir>/skill-sources/`

Each source clones into `<cacheDir>/<slug>/` where `slug` is the URL sanitized to a filesystem-safe, collision-resistant form (e.g. `github.com-obra-superpowers`).

### New file `internal/skills/source.go`

Same package as `skill.go`, reusing `Scan`/`parseSkill`/`Skill`:

```go
// Source is one configured remote git repository.
type Source struct {
    URL  string // git URL, exactly as configured
    Path string // subdir within the repo containing */SKILL.md (".", "skills", ...)
}

// SourceSkill is one installable skill discovered in a source repo.
type SourceSkill struct {
    Source      Source
    Name        string // frontmatter name (or directory-name fallback)
    Description string
    Dir         string // absolute path inside the local clone (copy source on install)
}

// SyncSources shallow-clones (or updates) each source into cacheDir. Returns a
// map from each Source to its local clone path. Per-source failures are
// non-fatal: the failed source is omitted from the map and logged via debug.
func SyncSources(ctx context.Context, cacheDir string, sources []Source) (map[Source]string, error)

// ScanSources scans each clone's <path> and returns the combined installable
// catalog. Reuses skills.Scan per clone.
func ScanSources(clones map[Source]string) []SourceSkill

// Search returns up to limit SourceSkills whose name or description contains
// query (case-insensitive). Name matches rank before description matches.
func Search(catalog []SourceSkill, query string, limit int) []SourceSkill

// Install copies the skill directory at match.Dir into skillsDir and parses
// the SKILL.md. If a directory named skillsDir/<name> already exists it is a
// no-op: the existing SKILL.md body is returned (idempotent). The destination
// name is derived from match.Name (validated), never from free-form input.
func Install(match SourceSkill, skillsDir string) (Skill, error)
```

**git invocation** (in `SyncSources`): use `exec.CommandContext(ctx, "git", args...)` — argument-based, never a shell. Per source:
1. slug = `sourceSlug(s.URL)`; `clonePath = filepath.Join(cacheDir, slug)`.
2. If `clonePath` does not exist: `git clone --depth 1 <url> <clonePath>`.
3. If it exists: `git -C <clonePath> pull --ff-only`. On failure, remove `clonePath` and re-clone (cache is regenerable).
4. `os/exec.LookPath("git")` is checked once up front; if missing, `SyncSources` returns an error which the tool surfaces as a user-facing message.

`sourceSlug` sanitizes the URL: strip scheme, drop trailing `.git`, replace `/`/`:`/`@` with `-`, collapse repeats. Deterministic and filesystem-safe on all platforms.

**Install copy**: walk `match.Dir`, recreate the tree under `filepath.Join(skillsDir, filepath.Base(match.Dir))`, copy file contents. The destination base name equals the scanned skill's directory name; reject if it contains path separators or `..` (defense-in-depth; it should never, since it came from `Scan`, but validate anyway).

### New tool `internal/tools/skill_install.go`

Two registrations, mirroring `RegisterSkill`'s shape:

```go
// RegisterSearchSkill registers the search_skills tool (read-only). sources is
// the configured source list; the tool lazily syncs+scans on first call and
// caches the result in the closure-captured *cache.
func RegisterSearchSkill(reg *Registry, sources []skills.Source, cache *[]skills.SourceSkill) error

// RegisterInstallSkill registers the install_skill tool (write; requires
// approval). Shares the same *cache as search_skills so either call warms it.
func RegisterInstallSkill(reg *Registry, sources []skills.Source, skillsDir string, cache *[]skills.SourceSkill) error
```

The two tools share a single lazily-populated `[]SourceSkill` (passed by pointer) so that a `search_skills` call warms the cache for a subsequent `install_skill`, and vice-versa. Both trigger `SyncSources` if `*cache` is nil.

#### `search_skills`

| Field | Value |
|---|---|
| Name | `search_skills` |
| Description | `Search remote skill sources for installable skills matching a query. Sources are configured via the "skill-sources" config key. Returns each match's name, description, and source. Call install_skill to install one.` |
| Input schema | `{ "type":"object", "required":["query"], "properties":{ "query":{ "type":"string", "description":"Keywords to match against skill name and description (case-insensitive)." } } }` |
| `Capabilities.ReadOnly` | `true` |
| `TimeoutPolicy` | `TimeoutPolicyCaller` |
| `Kind` | `ToolKindBuiltin` |

**Behavior:**
1. Ensure cache populated (`SyncSources` + `ScanSources` if nil). Network/git errors → return a user-facing message (e.g. `"git not found; skill sources require git on PATH"` or `"could not sync skill sources: <error>"`) and do **not** cache a failure.
2. `Search(cache, query, 10)`.
3. Render each match: `<name> — <description> (source: <repo-short>)`. Append `(installed)` if the skill already exists locally in `skillsDir`. No matches → `"no skills found for '<query>'"`. Empty catalog after sync → `"no skills found in <n> source(s)"`.

`ReadOnly=true`: the tool touches only the mods-internal cache dir and the network, never the user's workspace — consistent with `load_skill` being read-only despite reading disk. It does **not** go through `requestApproval`.

#### `install_skill`

| Field | Value |
|---|---|
| Name | `install_skill` |
| Description | `Install a skill from a configured remote source into the local skills directory and return its instructions. The user must approve. After install the skill is also available to load_skill in future sessions.` |
| Input schema | `{ "type":"object", "required":["name"], "properties":{ "name":{ "type":"string", "description":"Skill name as returned by search_skills." }, "source":{ "type":"string", "description":"Optional. Source repo short name or URL to disambiguate when the same name exists in multiple sources. Omit when unique." } } }` |
| `Capabilities.ReadOnly` | `false` (write → approval) |
| `TimeoutPolicy` | `TimeoutPolicyCaller` |
| `Kind` | `ToolKindBuiltin` |

**Behavior:**
1. Ensure cache populated (same lazy sync as search).
2. Locate matches by name in the cache. If `source` given, narrow to that source (match by slug-short or full URL). Zero matches → `"skill not found in sources: <name>. Use search_skills to find available skills."`. Multiple matches across sources and `source` omitted → return the candidate list with each match's source so the LLM can disambiguate.
3. **Approval**: `ReadOnly=false` writes to `skillsDir` (outside the workspace) → the existing review flow in `request_session.go` / `requestApproval` must prompt the user. The review banner shows source, skill name, and description; the tool description already states the skill's instructions will be followed. The exact access-intent classification for a write to the user-skills-dir is finalized in the implementation plan (it routes to `DecisionAsk`).
4. `Install(match, skillsDir)`:
   - Already installed (`skillsDir/<name>` exists) → idempotent: parse and return the on-disk SKILL.md body, no copy.
   - Otherwise copy the directory and return the parsed body.
5. Return the body as tool content (immediate use; the LLM need not call `load_skill`).

### Integration points

**`internal/app/mods.go`** — No new field required. The existing `skillCatalog` (local skills) is unchanged. `cfg.SkillSources` is read at the `BuildRegistry` call site (wherever `cfg.SkillDir`/`skillCatalog` are already passed), so no caching of sources on `Mods` is needed — they are pure config.

**`internal/tooling/tools.go` `BuildRegistry`** — Add a `skillSources []skills.Source` parameter (explicit, like `skillCatalog`). When `len(skillSources) > 0`, create a local `var sourceCache []skills.SourceSkill` and register both `search_skills` and `install_skill`, passing `&sourceCache` to each so they share one lazily-populated cache via pointer. Update all callers (`internal/app/stream.go` or wherever `BuildRegistry` is called) to pass `cfg.SkillSources`. When the slice is empty, skip both so the tool surface stays clean.

**`internal/tooling/tools.go` `BuiltinSpecs`** — Register both tools with zero-value args (`RegisterSearchSkill(registry, nil, nil)` and `RegisterInstallSkill(registry, nil, "", nil)`) so they appear in `--list-tools`. The `Call` closures are never invoked in this path, so nil sources/dir/cache are safe — mirroring the existing `RegisterSkill(registry, nil)`.

**`internal/prompts/identity.md`** — Add a short subsection under `## Skills` explaining `search_skills` / `install_skill` and the `skill-sources` config key. Extend the config-keys list with `skill-sources`.

## Failure Handling

| Situation | Behavior |
|---|---|
| `git` not on PATH | `SyncSources` returns error; `search_skills`/`install_skill` surface `"git not found; skill sources require git on PATH"` |
| Clone fails (network/auth/unreachable) | Per-source: omit from map, log via `debug`; search proceeds over remaining sources; if all fail, surface `"could not sync skill sources: <error>"` |
| `git pull --ff-only` fails (diverged/corrupt cache) | Delete the slug dir and re-clone `--depth 1`; if re-clone also fails, omit the source |
| Source `path` does not exist in clone | `Scan` returns nil for that clone (no skills); source contributes nothing |
| No search matches | `"no skills found for '<query>'"` |
| All sources scanned but catalog empty | `"no skills found in <n> source(s)"` |
| `install_skill("nonexistent")` | `"skill not found in sources: <name>. Use search_skills to find available skills."` |
| `install_skill` name collision across sources, `source` omitted | Return candidate list with each source; no copy |
| `install_skill` approved but copy fails (disk/permission) | Return `"could not install skill: <error>"`; destination left untouched |
| Skill already installed | Idempotent: return existing on-disk body, no copy |
| User declines approval | Return the decline message; nothing written |
| Minimal mode | Tools not registered (consistent with `load_skill`/identity skipping) |
| `skill-sources` empty/not configured | Tools not registered; no error |

All diagnostics go through the existing `debug` logger (visible only under `MODS_DEBUG`); user-facing tool outputs are the strings in the table above.

## Security Model

| Threat | Mitigation |
|---|---|
| LLM injects an arbitrary git URL to clone | **Impossible by construction.** The LLM only ever supplies `query` (search) and `name`/`source` (install). Source URLs come exclusively from user config. git is invoked with `exec` args, never a shell — no argument injection. |
| Installed skill body is untrusted prompt instructions | Same trust model as today's manual `cp -r` of a skill dir. `install_skill` is `ReadOnly=false` and **must** pass `requestApproval`; the review banner surfaces source + name + description and notes the instructions will be followed. `load_skill` (approval-free) only serves already-installed *local* skills. |
| Destination path escape | Destination name is the scanned skill's directory name (from `Scan`, a real dir entry), validated to contain no separators/`..`; `filepath.Join(skillsDir, name)` with a prefix containment check, reusing the `readAuxFile`-style defense from `skill.go`. |
| Cache dir corruption/tampering | Cache is regenerable (hence `CacheHome`); a failed `pull --ff-only` triggers delete-and-reclone. |
| Search results contain injection text (remote name/desc) | Results are presented to the LLM as data only; acting on them is gated by install approval + load. |
| SSRF via source URL | Out of scope to block: source URLs are user-configured local inputs (user's own choice), not LLM/remote-derived. Distinct from the `MODS_WEB_SEARCH_ALLOW_PRIVATE` SSRF guard, which protects against LLM-supplied web-search targets. |

**Key invariants:** install is the *only* action that writes to the user skills dir, and it always requires approval. Search/clone-to-cache is a mods-internal regenerable operation (analogous to session-DB writes) — it does not touch the workspace and does not go through tool approval, though it does reach the network via parameterized `git`.

## Testing

### `internal/skills/source_test.go`

- `sourceSlug`: determinism, scheme-stripping, `.git` trimming, special-char sanitization, cross-platform safety.
- `SyncSources`: use a temp local bare repo (`git init --bare`) as the `url` so `git clone --depth 1` works offline in tests; fresh clone path; existing-clone `pull` update path; `pull` failure → delete + reclone fallback; `git` missing → error (skip if `git` absent via `testing.Short`/`exec.LookPath` guard).
- `ScanSources`: multi-source merge; `path` subdir resolution (`skills/` vs `.`); same name across two sources coexists.
- `Search`: case-insensitivity; name-hit ranks before description-hit; `limit` truncation; no match → empty.
- `Install`: copies full dir including `scripts/`/`reference/` aux files; returns parsed `Skill` with body; already-exists → idempotent (no overwrite, returns existing body); destination name validation rejects separator/`..`.

### `internal/tools/skill_install_test.go`

- `search_skills`: matches render correctly with `(source:)` + `(installed)`; no-match message; empty-catalog message; sync-failure message is not cached (a later successful sync recovers).
- `install_skill`: success returns body; collision-without-source returns candidates; not-found suggests `search_skills`; idempotent re-install; shared cache warmed by a prior search call.

### `internal/config/config_test.go`

- `skill-sources` default value (one entry, superpowers, path `skills`).
- YAML parsing with explicit `path` and with omitted `path` (defaults `.`).
- Portable-mode cache-dir derivation (`defaultSkillSourcesCacheDir`).

### `internal/app/*_test.go`

- With sources configured: `search_skills` + `install_skill` registered; `load_skill` registration unaffected.
- Without sources: neither new tool registered; tool surface unchanged.
- `install_skill` routes to `DecisionAsk`/review (using the existing review test harness).

### `internal/prompts/prompts_test.go`

- `TestIdentityCoversConfigKeys` extended to assert `skill-sources` is documented in identity.md.

## Out of Scope / Future Extensions

- **Uninstall / update tools** and **CLI subcommands** (`mods skills list/install/uninstall`).
- **Recursive source scanning** (point a source at a category-structured repo wholesale).
- **Full remote-catalog system-prompt injection** (mark remote skills "not installed" in the catalog instead of search-on-demand) — rejected to avoid system-prompt bloat.
- **Semantic/embedding search** over remote catalogs.
- **Pinned versions / SHAs** per source (today: always latest `master`/`main` tip via `--depth 1` + `pull --ff-only`).
- **Progress streaming** for clone (today: the normal spinner covers the synchronous clone; large repos are a one-time cost).

## Open Questions Resolved During Brainstorming

1. *Who triggers find-and-install?* → The LLM suggests; the user confirms via the review/approval flow (install is `ReadOnly=false`).
2. *Where do skills come from?* → A user-configurable list of git repos (`skill-sources`), defaulting to `obra/superpowers`.
3. *How does the LLM discover remote skills?* → On-demand `search_skills` tool (no system-prompt catalog bloat).
4. *When is an installed skill usable?* → Immediately: `install_skill` returns the body; no reload or restart needed.
5. *What's the management scope?* → Search + install only (YAGNI). No uninstall/update/CLI.
6. *How is the remote catalog obtained?* → Shallow-clone sources into `xdg.CacheHome/mods/skill-sources/` and reuse `skills.Scan` (source-agnostic, no API/token dependency; `git` is already a mods dependency).
7. *Cache location on Windows?* → `xdg.CacheHome` → `%LOCALAPPDATA%\cache\mods\skill-sources\` (adrg/xdg default), with portable-mode fallback to the executable directory.
8. *Does search require approval?* → No: read-only, analogous to `load_skill`.
9. *Already-installed install behavior?* → Idempotent: return the existing on-disk body without overwriting.
