# Skill Discovery & Installation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let mods search configurable remote git skill sources and install a skill on demand, returning the skill body immediately for in-session use.

**Architecture:** A `search_skills` tool (read-only) shallow-clones each configured `skill-sources` repo into `xdg.CacheHome/mods/skill-sources/`, scans them with the existing `skills.Scan`, and substring-matches the query. An `install_skill` tool (mutable, forces review) copies the matched skill dir into the user's `skills-dir` and returns the parsed body. Both share a lazily-populated catalog cache for the session.

**Tech Stack:** Go, `os/exec` (git CLI), `github.com/adrg/xdg`, `github.com/stretchr/testify/require`, mods' existing `internal/skills`, `internal/tools`, `internal/config`, `internal/approval` packages.

## Global Constraints

- Config precedence: CLI flags > mods.yml > MODS_ env > defaults. `skill-sources` is config-file-only (no CLI flag, no env), matching `mcp-servers`.
- Portable mode: the source-clone cache default is `<exe-dir>/skill-sources` when portable mode is active, else `xdg.CacheHome/mods/skill-sources` (on Windows `xdg.CacheHome` defaults to `%LOCALAPPDATA%\cache`).
- git is invoked via `exec.CommandContext` with argument slices — never a shell. The LLM never supplies a git URL; source URLs come only from user config.
- AGENTS.md rule: when adding a config-file-only key, update the config struct + `Help` map + `config_template.yml` + config tests + `internal/prompts/identity.md` together.
- Tests use `github.com/stretchr/testify/require`. Local lint is `golangci-lint run ./...`; build/test via `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check` then `... test`.
- No comments in code unless matching the existing doc-comment style on exported identifiers.

---

### Task 1: Config — `skill-sources` key, default source, and cache dir

**Files:**
- Modify: `internal/config/config.go` (struct field ~line 227; `Help` map ~line 104; `Ensure` ~lines 424-425 and ~471-472; defaults near ~line 580; export cache-dir)
- Modify: `internal/config/config_template.yml` (after ~line 146)
- Test: `internal/config/config_test.go` (append after ~line 410)

**Interfaces:**
- Consumes: nothing new.
- Produces: `config.SkillSource` struct; `config.Config.SkillSources []SkillSource` (yaml `skill-sources`); `config.DefaultSkillSourcesCacheDir() string`; `applySkillSourcesDefault(*Config)`. Later tasks use `cfg.SkillSources` and `cfgpkg.DefaultSkillSourcesCacheDir()`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/config_test.go`:

```go
func TestSkillSourcesDefault(t *testing.T) {
	cfg := Default()
	applySkillSourcesDefault(&cfg)
	require.Len(t, cfg.SkillSources, 1)
	require.Equal(t, "https://github.com/obra/superpowers.git", cfg.SkillSources[0].URL)
	require.Equal(t, "skills", cfg.SkillSources[0].Path)
}

func TestSkillSourcesYAMLOverride(t *testing.T) {
	yamlContent := "skill-sources:\n  - url: https://example.com/a.git\n    path: subdir\n  - url: https://example.com/b.git\n"
	var cfg Config
	require.NoError(t, yaml.Unmarshal([]byte(yamlContent), &cfg))
	applySkillSourcesDefault(&cfg)
	require.Len(t, cfg.SkillSources, 2)
	require.Equal(t, "subdir", cfg.SkillSources[0].Path)
	require.Equal(t, ".", cfg.SkillSources[1].Path) // empty path normalized to "."
}

func TestDefaultSkillSourcesCacheDir(t *testing.T) {
	dir := DefaultSkillSourcesCacheDir()
	require.NotEmpty(t, dir)
	require.Contains(t, dir, filepath.Join("mods", "skill-sources"))
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/config -run TestSkillSources -count=1` and `go test ./internal/config -run TestDefaultSkillSourcesCacheDir -count=1`
Expected: FAIL — `cfg.SkillSources` undefined, `applySkillSourcesDefault` undefined, `DefaultSkillSourcesCacheDir` undefined.

- [ ] **Step 3: Add the `SkillSource` type and `SkillSources` field**

In `internal/config/config.go`, just above `type PersistentConfig struct {` (line 193), add:

```go
// SkillSource is one configured remote git repository that mods can search
// and install skills from.
type SkillSource struct {
	URL  string `yaml:"url"`
	Path string `yaml:"path"`
}
```

Inside `PersistentConfig`, right after the `SkillsDir` field (line 227), add:

```go
	SkillSources []SkillSource `yaml:"skill-sources"`
```

- [ ] **Step 4: Add `defaultSkillSources`, `applySkillSourcesDefault`, and the exported cache-dir function**

In `internal/config/config.go`, right after `applySkillsDirDefault` (ends ~line 590), add:

```go
// defaultSkillSources returns the built-in list of remote skill sources used
// when the user has not configured any.
func defaultSkillSources() []SkillSource {
	return []SkillSource{
		{URL: "https://github.com/obra/superpowers.git", Path: "skills"},
	}
}

// applySkillSourcesDefault ensures c.SkillSources is non-empty, applying the
// built-in default when the user has not configured any source. Each source is
// normalized: an empty Path becomes "." and trailing slashes are stripped from
// the URL. Idempotent, mirroring applySkillsDirDefault.
func applySkillSourcesDefault(c *Config) {
	if len(c.SkillSources) == 0 {
		c.SkillSources = defaultSkillSources()
	}
	for i := range c.SkillSources {
		if c.SkillSources[i].Path == "" {
			c.SkillSources[i].Path = "."
		}
		c.SkillSources[i].URL = stdstrings.TrimRight(c.SkillSources[i].URL, "/")
	}
}

// DefaultSkillSourcesCacheDir resolves the directory where remote skill source
// repos are shallow-cloned. It is regenerable, so it lives under the cache
// home. Portable mode: next to the executable (self-contained), mirroring the
// skills/sessions defaults.
func DefaultSkillSourcesCacheDir() string {
	if portableActive() {
		return filepath.Join(executableDir(), "skill-sources")
	}
	return filepath.Join(xdg.CacheHome, "mods", "skill-sources")
}
```

- [ ] **Step 5: Call `applySkillSourcesDefault` in `Ensure`**

In `Ensure`, next to the two `applySkillsDirDefault` call pairs. After line 425 (`applySkillsDirDefault(&fallback)`) add:

```go
	applySkillSourcesDefault(&c)
	applySkillSourcesDefault(&fallback)
```

And after line 472 (the second `applySkillsDirDefault(&c)`) add:

```go
	applySkillSourcesDefault(&c)
```

- [ ] **Step 6: Add the `Help` map entry**

In the `Help` map, right after the `"skills-dir"` entry (line 104), add (keeping alphabetical/gofmt alignment — run gofmt after):

```go
	"skill-sources":          "List of remote git repositories mods can search and install skills from. Each entry has a git 'url' and an optional 'path' (subdir containing skill directories; defaults to the repo root).",
```

- [ ] **Step 7: Add the template entry**

In `internal/config/config_template.yml`, right after the `# skills-dir:` block (line 146), add:

```yaml

# {{ index .Help "skill-sources" }}
# skill-sources:
#   - url: https://github.com/obra/superpowers.git
#     path: skills
```

- [ ] **Step 8: Run tests to verify they pass**

Run: `go test ./internal/config -run TestSkillSources -count=1 -v` and `go test ./internal/config -run TestDefaultSkillSourcesCacheDir -count=1 -v`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_template.yml internal/config/config_test.go
git commit -m "feat(config): add skill-sources config key with default and cache dir"
```

---

### Task 2: Source repo sync — types, slug, and `SyncSources`

**Files:**
- Create: `internal/skills/source.go`
- Test: `internal/skills/source_test.go`

**Interfaces:**
- Consumes: `skills.Scan` (from `skill.go`, same package); `internal/debug`.
- Produces: `skills.Source`, `skills.SourceSkill`, `skills.SyncSources(ctx, cacheDir, sources) (map[Source]string, error)`. Task 3 reuses `Source`/`SourceSkill`.

- [ ] **Step 1: Write the failing tests**

Create `internal/skills/source_test.go`:

```go
package skills

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSourceSlug(t *testing.T) {
	cases := map[string]string{
		"https://github.com/obra/superpowers.git": "github.com-obra-superpowers",
		"git@github.com:obra/superpowers.git":      "github.com-obra-superpowers",
		"https://example.com/a/b.git":              "example.com-a-b",
		"https://example.com/a/b/":                 "example.com-a-b",
	}
	for in, want := range cases {
		require.Equal(t, want, sourceSlug(in), "input %q", in)
	}
}

// makeSourceRepo creates a local git repo at path containing
// skills/<skillName>/SKILL.md with the given body. Used as a clone source.
func makeSourceRepo(t *testing.T, path, skillName, body string) {
	t.Helper()
	skillDir := filepath.Join(path, "skills", skillName)
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(body), 0o600))
	runGit(t, path, "init")
	runGit(t, path, "add", "-A")
	runGit(t, path, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-m", "init")
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{"-C", dir, "-c", "safe.directory=" + dir}, args...)
	require.NoError(t, exec.Command("git", full...).Run(), "git %v", args)
}

func TestSyncSourcesClonesIntoCache(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	makeSourceRepo(t, repo, "foo", "---\nname: foo\ndescription: Foo.\n---\n\nbody.\n")
	cache := t.TempDir()
	src := Source{URL: repo, Path: "skills"}
	clones, err := SyncSources(context.Background(), cache, []Source{src})
	require.NoError(t, err)
	require.Contains(t, clones, src)
	// Files exist in the clone.
	require.FileExists(t, filepath.Join(clones[src], "skills", "foo", "SKILL.md"))
}

func TestSyncSourcesUpdatesExistingClone(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	repo := t.TempDir()
	makeSourceRepo(t, repo, "foo", "---\nname: foo\ndescription: Foo.\n---\n\nbody.\n")
	cache := t.TempDir()
	src := Source{URL: repo, Path: "skills"}
	_, err := SyncSources(context.Background(), cache, []Source{src})
	require.NoError(t, err)
	// Second sync reuses the clone (pull --ff-only path).
	clones, err := SyncSources(context.Background(), cache, []Source{src})
	require.NoError(t, err)
	require.Contains(t, clones, src)
}

func TestSyncSourcesSkipsUnreachableSource(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	cache := t.TempDir()
	src := Source{URL: "file:///nonexistent-repo-xyz-12345", Path: "."}
	clones, err := SyncSources(context.Background(), cache, []Source{src})
	require.NoError(t, err) // per-source failure is non-fatal
	require.Empty(t, clones)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/skills -run TestSourceSlug -count=1` and `go test ./internal/skills -run TestSyncSources -count=1`
Expected: FAIL — `sourceSlug`, `SyncSources`, `gitAvailable`, `Source` undefined.

- [ ] **Step 3: Create `source.go` with types, slug, and `SyncSources`**

Create `internal/skills/source.go`:

```go
package skills

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/debug"
)

// Source is one configured remote git repository that mods can search and
// install skills from.
type Source struct {
	URL  string // git URL, exactly as configured
	Path string // subdir within the repo containing */SKILL.md (".", "skills", ...)
}

// SourceSkill is one installable skill discovered in a source repo.
type SourceSkill struct {
	Source      Source
	Name        string
	Description string
	Dir         string // absolute path of the skill directory inside the local clone
}

// sourceSlug turns a git URL into a filesystem-safe, deterministic directory
// name. The scheme (or scp-like user@) and a trailing ".git" are stripped;
// '/', ':', '@' become '-' and repeats collapse.
func sourceSlug(url string) string {
	s := url
	if i := strings.Index(s, "://"); i >= 0 {
		s = s[i+3:]
	} else if i := strings.Index(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ".git")
	s = strings.TrimRight(s, "/")
	s = strings.NewReplacer("/", "-", ":", "-", "@", "-", " ", "-").Replace(s)
	for strings.Contains(s, "--") {
		s = strings.ReplaceAll(s, "--", "-")
	}
	return strings.Trim(s, "-")
}

// gitAvailable reports whether git is on PATH.
func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

// SyncSources shallow-clones (or updates) each source into cacheDir and
// returns a map from each successfully-synced Source to its local clone path.
// Per-source failures are non-fatal: the failed source is omitted and logged.
// Returns an error only if git is missing or the cache dir cannot be created.
func SyncSources(ctx context.Context, cacheDir string, sources []Source) (map[Source]string, error) {
	if !gitAvailable() {
		return nil, fmt.Errorf("git not found on PATH; skill sources require git")
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, fmt.Errorf("could not create skill sources cache dir %q: %w", cacheDir, err)
	}
	clones := make(map[Source]string, len(sources))
	for _, src := range sources {
		clonePath := filepath.Join(cacheDir, sourceSlug(src.URL))
		if err := syncOne(ctx, src.URL, clonePath); err != nil {
			debug.Printf("skills: sync source %q failed: %v (skipping)", src.URL, err)
			continue
		}
		clones[src] = clonePath
	}
	return clones, nil
}

// syncOne clones clonePath if absent, otherwise pulls. A failed pull triggers
// a delete-and-reclone fallback (the cache is regenerable).
func syncOne(ctx context.Context, url, clonePath string) error {
	if _, err := os.Stat(clonePath); err == nil {
		if err := gitPull(ctx, clonePath); err == nil {
			return nil
		}
		_ = os.RemoveAll(clonePath)
	}
	return gitClone(ctx, url, clonePath)
}

func gitClone(ctx context.Context, url, dir string) error {
	out, err := exec.CommandContext(ctx, "git", "clone", "--depth", "1", url, dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git clone %s: %w: %s", url, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func gitPull(ctx context.Context, dir string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "pull", "--ff-only").CombinedOutput()
	if err != nil {
		return fmt.Errorf("git pull: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/skills -run 'TestSourceSlug|TestSyncSources' -count=1 -v`
Expected: PASS (the unreachable-source test skips the source and returns an empty map).

- [ ] **Step 5: Commit**

```bash
git add internal/skills/source.go internal/skills/source_test.go
git commit -m "feat(skills): add Source types and SyncSources for remote skill repos"
```

---

### Task 3: Source catalog scan, search, and install

**Files:**
- Modify: `internal/skills/source.go` (append functions)
- Test: `internal/skills/source_test.go` (append tests)

**Interfaces:**
- Consumes: `skills.Scan`, `skills.parseSkill` (same package); `Source`/`SourceSkill` from Task 2.
- Produces: `skills.ScanSources(map[Source]string) []SourceSkill`, `skills.Search(catalog, query, limit) []SourceSkill`, `skills.Install(match, skillsDir) (Skill, error)`. Task 4 (tools) consumes all three plus `Skill`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/skills/source_test.go`:

```go
func TestScanSources(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, filepath.Join(root, "skills"), "foo", "---\nname: foo\ndescription: Foo skill.\n---\n\nBody.\n")
	writeSkill(t, filepath.Join(root, "skills"), "bar", "---\nname: bar\ndescription: Bar skill.\n---\n\nBody.\n")
	src := Source{URL: "https://example.com/r.git", Path: "skills"}
	got := ScanSources(map[Source]string{src: root})
	require.Len(t, got, 2)
	names := []string{got[0].Name, got[1].Name}
	require.ElementsMatch(t, []string{"foo", "bar"}, names)
}

func TestSearchRanksNameHitsFirst(t *testing.T) {
	cat := []SourceSkill{
		{Name: "alpha", Description: "mentions test here"},
		{Name: "other", Description: "a test description"},
	}
	got := Search(cat, "test", 10)
	require.Len(t, got, 2)
	require.Equal(t, "other", got[0].Name) // name hit ranks first
	require.Equal(t, "alpha", got[1].Name)
}

func TestSearchLimitTruncates(t *testing.T) {
	cat := []SourceSkill{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	got := Search(cat, "", 2) // empty query returns all, truncated
	require.Len(t, got, 2)
}

func TestSearchNoMatch(t *testing.T) {
	cat := []SourceSkill{{Name: "alpha", Description: "x"}}
	require.Empty(t, Search(cat, "zzz", 10))
}

func TestInstallCopiesAndParses(t *testing.T) {
	srcRoot := t.TempDir()
	writeSkill(t, srcRoot, "my", "---\nname: my\ndescription: My.\n---\n\nMy body.\n")
	require.NoError(t, os.MkdirAll(filepath.Join(srcRoot, "my", "reference"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "my", "reference", "x.md"), []byte("aux"), 0o600))
	match := SourceSkill{Source: Source{URL: "u", Path: "."}, Name: "my", Dir: filepath.Join(srcRoot, "my")}
	skillsDir := t.TempDir()
	skill, err := Install(match, skillsDir)
	require.NoError(t, err)
	require.Equal(t, "My body.", skill.Body)
	data, err := os.ReadFile(filepath.Join(skillsDir, "my", "reference", "x.md"))
	require.NoError(t, err)
	require.Equal(t, "aux", string(data))
}

func TestInstallIdempotent(t *testing.T) {
	srcRoot := t.TempDir()
	writeSkill(t, srcRoot, "my", "---\nname: my\n---\n\nOriginal.\n")
	match := SourceSkill{Name: "my", Dir: filepath.Join(srcRoot, "my")}
	skillsDir := t.TempDir()
	s1, err := Install(match, skillsDir)
	require.NoError(t, err)
	// Change the source body; a re-install must NOT overwrite (idempotent).
	require.NoError(t, os.WriteFile(filepath.Join(srcRoot, "my", "SKILL.md"), []byte("---\nname: my\n---\n\nChanged.\n"), 0o600))
	s2, err := Install(match, skillsDir)
	require.NoError(t, err)
	require.Equal(t, "Original.", s2.Body)
	require.Equal(t, s1.Body, s2.Body)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/skills -run 'TestScanSources|TestSearch|TestInstall' -count=1`
Expected: FAIL — `ScanSources`, `Search`, `Install` undefined.

- [ ] **Step 3: Implement `ScanSources`, `Search`, and `Install`**

Append to `internal/skills/source.go`:

```go
// ScanSources scans each clone's configured <path> and returns the combined
// installable catalog. Reuses skills.Scan per clone. Clones whose path yields
// no skills (or fails to scan) contribute nothing.
func ScanSources(clones map[Source]string) []SourceSkill {
	var out []SourceSkill
	for src, clonePath := range clones {
		found, err := Scan(filepath.Join(clonePath, src.Path))
		if err != nil {
			debug.Printf("skills: scan source %q failed: %v", src.URL, err)
			continue
		}
		for _, s := range found {
			out = append(out, SourceSkill{
				Source:      src,
				Name:        s.Name,
				Description: s.Description,
				Dir:         s.Dir,
			})
		}
	}
	return out
}

// Search returns up to limit SourceSkills whose name or description contains
// query (case-insensitive). An empty query returns all (browsing). Name
// matches always rank before description-only matches.
func Search(catalog []SourceSkill, query string, limit int) []SourceSkill {
	q := strings.ToLower(query)
	var nameHits, descHits []SourceSkill
	for _, s := range catalog {
		if q == "" || strings.Contains(strings.ToLower(s.Name), q) {
			nameHits = append(nameHits, s)
			continue
		}
		if strings.Contains(strings.ToLower(s.Description), q) {
			descHits = append(descHits, s)
		}
	}
	merged := append(nameHits, descHits...)
	if limit > 0 && len(merged) > limit {
		merged = merged[:limit]
	}
	return merged
}

// Install copies the skill directory at match.Dir into skillsDir and parses
// the SKILL.md. If skillsDir/<name> already exists it is a no-op: the existing
// SKILL.md body is returned (idempotent). The destination name is derived from
// the skill's directory name and validated.
func Install(match SourceSkill, skillsDir string) (Skill, error) {
	destName := filepath.Base(match.Dir)
	if !validDestName(destName) {
		return Skill{}, fmt.Errorf("invalid skill directory name: %s", destName)
	}
	dest := filepath.Join(skillsDir, destName)
	if info, err := os.Stat(dest); err == nil {
		if !info.IsDir() {
			return Skill{}, fmt.Errorf("destination exists and is not a directory: %s", dest)
		}
		return parseSkillFile(dest)
	}
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return Skill{}, fmt.Errorf("could not create skills dir %q: %w", skillsDir, err)
	}
	if err := copyDir(match.Dir, dest); err != nil {
		return Skill{}, fmt.Errorf("could not install skill %q: %w", match.Name, err)
	}
	return parseSkillFile(dest)
}

func validDestName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, filepath.Separator) {
		return false
	}
	return name != "\\"
}

func parseSkillFile(skillDir string) (Skill, error) {
	data, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
	if err != nil {
		return Skill{}, fmt.Errorf("could not read SKILL.md in %q: %w", skillDir, err)
	}
	skill, err := parseSkill(string(data), skillDir)
	if err != nil {
		return Skill{}, err
	}
	return skill, nil
}

func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/skills -count=1 -v`
Expected: PASS (all source tests, including Task 2's).

- [ ] **Step 5: Commit**

```bash
git add internal/skills/source.go internal/skills/source_test.go
git commit -m "feat(skills): add ScanSources, Search, and Install for remote skills"
```

---

### Task 4: `search_skills` and `install_skill` tools

**Files:**
- Create: `internal/tools/skill_install.go`
- Test: `internal/tools/skill_install_test.go`

**Interfaces:**
- Consumes: `skills.Source`, `skills.SourceSkill`, `skills.SyncSources`, `skills.ScanSources`, `skills.Search`, `skills.Install`; `tools.Registry`/`Tool`/`objectSchema`/`stringProp`/`decodeArgs` (same package); `approval.AccessIntent`/`AccessWrite`.
- Produces: `tools.SourceCache`, `tools.NewSourceCache()`, `tools.SkillInstallConfig`, `tools.RegisterSearchSkill(reg, cfg)`, `tools.RegisterInstallSkill(reg, cfg)`. Task 5 consumes all of these.

- [ ] **Step 1: Write the failing tests**

Create `internal/tools/skill_install_test.go`:

```go
package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

// prewarmedCache builds a SourceCache already populated (synced) so tests
// avoid hitting git/network.
func prewarmedCache(t *testing.T, skillsDir string) (*SourceCache, []skills.Source) {
	t.Helper()
	srcRepo := t.TempDir()
	writeSkillAt(t, filepath.Join(srcRepo, "skills"), "alpha",
		"---\nname: alpha\ndescription: Alpha testing skill.\n---\n\nAlpha body.\n")
	writeSkillAt(t, filepath.Join(srcRepo, "skills"), "beta",
		"---\nname: beta\ndescription: Beta helper.\n---\n\nBeta body.\n")
	catalog, err := skills.Scan(filepath.Join(srcRepo, "skills"))
	require.NoError(t, err)
	var sc []skills.SourceSkill
	src := skills.Source{URL: "https://example.com/repo.git", Path: "skills"}
	for _, s := range catalog {
		sc = append(sc, skills.SourceSkill{Source: src, Name: s.Name, Description: s.Description, Dir: s.Dir})
	}
	cache := &SourceCache{catalog: sc, synced: true}
	return cache, []skills.Source{src}
}

// writeSkillAt writes a SKILL.md inside base/<name>/.
func writeSkillAt(t *testing.T, base, name, content string) {
	t.Helper()
	dir := filepath.Join(base, name)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o600))
}

func installToolCfg(cache *SourceCache, sources []skills.Source, skillsDir string) SkillInstallConfig {
	return SkillInstallConfig{Sources: sources, CacheDir: "", SkillsDir: skillsDir, Cache: cache}
}

func TestSearchSkillsReturnsMatches(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	reg := NewRegistry()
	require.NoError(t, RegisterSearchSkill(reg, installToolCfg(cache, sources, t.TempDir())))
	out, err := reg.Call(context.Background(), "search_skills", []byte(`{"query":"testing"}`))
	require.NoError(t, err)
	require.Contains(t, out, "alpha")
	require.Contains(t, out, "source:")
}

func TestSearchSkillsNoMatch(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	reg := NewRegistry()
	require.NoError(t, RegisterSearchSkill(reg, installToolCfg(cache, sources, t.TempDir())))
	out, err := reg.Call(context.Background(), "search_skills", []byte(`{"query":"zzz"}`))
	require.NoError(t, err)
	require.Contains(t, out, "no skills found")
}

func TestSearchSkillsMarksInstalled(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	skillsDir := t.TempDir()
	// Pre-create an installed alpha.
	writeSkillAt(t, skillsDir, "alpha", "---\nname: alpha\n---\n\ninstalled.\n")
	reg := NewRegistry()
	require.NoError(t, RegisterSearchSkill(reg, installToolCfg(cache, sources, skillsDir)))
	out, err := reg.Call(context.Background(), "search_skills", []byte(`{"query":"alpha"}`))
	require.NoError(t, err)
	require.Contains(t, out, "(installed)")
}

func TestInstallSkillReturnsBody(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	skillsDir := t.TempDir()
	reg := NewRegistry()
	require.NoError(t, RegisterInstallSkill(reg, installToolCfg(cache, sources, skillsDir)))
	out, err := reg.Call(context.Background(), "install_skill", []byte(`{"name":"alpha"}`))
	require.NoError(t, err)
	require.Equal(t, "Alpha body.", out)
	// Files landed on disk.
	require.FileExists(t, filepath.Join(skillsDir, "alpha", "SKILL.md"))
}

func TestInstallSkillNotFound(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	reg := NewRegistry()
	require.NoError(t, RegisterInstallSkill(reg, installToolCfg(cache, sources, t.TempDir())))
	out, err := reg.Call(context.Background(), "install_skill", []byte(`{"name":"nonexistent"}`))
	require.NoError(t, err)
	require.Contains(t, out, "skill not found in sources")
	require.Contains(t, out, "search_skills")
}

func TestInstallSkillIdempotent(t *testing.T) {
	cache, sources := prewarmedCache(t, t.TempDir())
	skillsDir := t.TempDir()
	reg := NewRegistry()
	require.NoError(t, RegisterInstallSkill(reg, installToolCfg(cache, sources, skillsDir)))
	first, err := reg.Call(context.Background(), "install_skill", []byte(`{"name":"alpha"}`))
	require.NoError(t, err)
	second, err := reg.Call(context.Background(), "install_skill", []byte(`{"name":"alpha"}`))
	require.NoError(t, err)
	require.Equal(t, first, second)
}

func TestRegisterInstallSkillIsMutable(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterInstallSkill(reg, SkillInstallConfig{Cache: NewSourceCache()}))
	tool, ok := reg.Tool("install_skill")
	require.True(t, ok)
	require.True(t, tool.Capabilities.Mutable, "install_skill must be Mutable to force review")
	require.False(t, tool.Capabilities.ReadOnly)
}

func TestRegisterSearchSkillIsReadOnly(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterSearchSkill(reg, SkillInstallConfig{Cache: NewSourceCache()}))
	tool, ok := reg.Tool("search_skills")
	require.True(t, ok)
	require.True(t, tool.Capabilities.ReadOnly)
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/tools -run 'TestSearchSkills|TestInstallSkill|TestRegisterInstallSkill|TestRegisterSearchSkill' -count=1`
Expected: FAIL — `SourceCache`, `SkillInstallConfig`, `RegisterSearchSkill`, `RegisterInstallSkill` undefined.

- [ ] **Step 3: Create `skill_install.go`**

Create `internal/tools/skill_install.go`:

```go
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
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/tools -count=1 -v`
Expected: PASS (all tool tests, including the existing load_skill tests).

- [ ] **Step 5: Commit**

```bash
git add internal/tools/skill_install.go internal/tools/skill_install_test.go
git commit -m "feat(tools): add search_skills and install_skill tools"
```

---

### Task 5: Wire tools into `BuildRegistry`, `BuiltinSpecs`, and the call site

**Files:**
- Modify: `internal/tooling/tools.go` (`BuildRegistry` signature line 19; after the `load_skill` block ~line 73; `BuiltinSpecs` ~line 141)
- Modify: `internal/app/tool_support.go` (line 48)
- Modify: `internal/tooling/tools_test.go` (lines 74 and 82; append tests)
- Test: `internal/tooling/tools_test.go`

**Interfaces:**
- Consumes: `tools.RegisterSearchSkill`, `tools.RegisterInstallSkill`, `tools.NewSourceCache`, `tools.SkillInstallConfig` (Task 4); `cfgpkg.DefaultSkillSourcesCacheDir()`, `cfg.SkillSources`, `cfg.SkillsDir`.
- Produces: updated `BuildRegistry(..., skillSources []skills.Source)` signature; both tools registered when sources are configured.

- [ ] **Step 1: Write the failing tests**

Append to `internal/tooling/tools_test.go`:

```go
func TestBuildRegistryRegistersSkillToolsWhenSourcesNonEmpty(t *testing.T) {
	cfg := cfgpkg.Default()
	sources := []skills.Source{{URL: "https://example.com/s.git", Path: "."}}
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil, sources)
	require.NoError(t, err)
	_, okSearch := reg.Tool("search_skills")
	require.True(t, okSearch, "search_skills must be registered when sources are configured")
	_, okInstall := reg.Tool("install_skill")
	require.True(t, okInstall, "install_skill must be registered when sources are configured")
}

func TestBuildRegistrySkipsSkillToolsWhenSourcesEmpty(t *testing.T) {
	cfg := cfgpkg.Default()
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil, nil)
	require.NoError(t, err)
	_, okSearch := reg.Tool("search_skills")
	require.False(t, okSearch, "search_skills must NOT be registered when sources are empty")
	_, okInstall := reg.Tool("install_skill")
	require.False(t, okInstall)
}

func TestBuiltinSpecsIncludesSkillTools(t *testing.T) {
	specs, err := BuiltinSpecs()
	require.NoError(t, err)
	have := map[string]bool{}
	for _, s := range specs {
		have[s.Name] = true
	}
	require.True(t, have["search_skills"], "search_skills must appear in --list-tools")
	require.True(t, have["install_skill"], "install_skill must appear in --list-tools")
}
```

- [ ] **Step 2: Update the two existing `BuildRegistry` call sites in the test file**

In `internal/tooling/tools_test.go`, change the two `BuildRegistry(...)` calls so they compile against the new signature:

Line 74:
```go
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", catalog, nil)
```
Line 82:
```go
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil, nil)
```

- [ ] **Step 3: Run tests to verify they fail**

Run: `go test ./internal/tooling -count=1`
Expected: FAIL — `BuildRegistry` signature mismatch (too few args), and the new tools aren't registered yet.

- [ ] **Step 4: Update `BuildRegistry`**

In `internal/tooling/tools.go`, change the signature (line 19) to add `skillSources`:

```go
func BuildRegistry(ctx context.Context, cfg *cfgpkg.Config, wscfg websearch.Config, prompt string, skillCatalog []skills.Skill, skillSources []skills.Source) (*toolregistry.Registry, error) {
```

Right after the `load_skill` registration block (after line 73), add:

```go
	if len(skillSources) > 0 {
		installCfg := toolregistry.SkillInstallConfig{
			Sources:   skillSources,
			CacheDir:  cfgpkg.DefaultSkillSourcesCacheDir(),
			SkillsDir: cfg.SkillsDir,
			Cache:     toolregistry.NewSourceCache(),
		}
		if err := toolregistry.RegisterSearchSkill(registry, installCfg); err != nil {
			return nil, err
		}
		if err := toolregistry.RegisterInstallSkill(registry, installCfg); err != nil {
			return nil, err
		}
	}
```

- [ ] **Step 5: Update `BuiltinSpecs`**

In `internal/tooling/tools.go`, right after the existing `_ = toolregistry.RegisterSkill(registry, nil)` (line 141), add:

```go
	_ = toolregistry.RegisterSearchSkill(registry, toolregistry.SkillInstallConfig{Cache: toolregistry.NewSourceCache()})
	_ = toolregistry.RegisterInstallSkill(registry, toolregistry.SkillInstallConfig{Cache: toolregistry.NewSourceCache()})
```

- [ ] **Step 6: Update the production call site**

In `internal/app/tool_support.go` line 48, change to pass `cfg.SkillSources`:

```go
		return BuildRegistry(ctx, cfg, wscfg, prompt, m.skillCatalog, cfg.SkillSources)
```

- [ ] **Step 7: Run tests to verify they pass**

Run: `go test ./internal/tooling -count=1 -v` then `go test ./internal/app -count=1`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/tooling/tools.go internal/tooling/tools_test.go internal/app/tool_support.go
git commit -m "feat(tooling): wire search_skills/install_skill into BuildRegistry"
```

---

### Task 6: Document skills in `identity.md` and cover in tests

**Files:**
- Modify: `internal/prompts/identity.md` (config-keys list line 39; add subsection under `## Skills` ~line 55)
- Test: `internal/prompts/prompts_test.go` (the `TestIdentityCoversConfigKeys` list ~line 114)

**Interfaces:**
- Consumes: nothing code-level.
- Produces: identity.md documents `skill-sources`, `search_skills`, and `install_skill`; `TestIdentityCoversConfigKeys` asserts `skill-sources` is covered.

- [ ] **Step 1: Write the failing test assertion**

In `internal/prompts/prompts_test.go`, inside `TestIdentityCoversConfigKeys`, add `"skill-sources"` to the slice (after `"skills-dir"`, line 114):

```go
		"skills-dir",
		"skill-sources",
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/prompts -run TestIdentityCoversConfigKeys -count=1`
Expected: FAIL — identity.md does not contain `skill-sources`.

- [ ] **Step 3: Add `skill-sources` to the config-keys list in identity.md**

In `internal/prompts/identity.md`, update the config-keys line (line 39) to append `skill-sources`:

```
Config-file-only keys (no CLI flag): `apis`, `roles`, `prompts`, `mcp-servers`,
`mcp-timeout`, `builtin-tools`, `format-text`, `shell-classify-prompt`, `max-input-chars`, `skills-dir`, `skill-sources`.
```

- [ ] **Step 4: Document the tools in the `## Skills` section**

In `internal/prompts/identity.md`, after the existing Skills paragraph ending "Skip skills when their description does not match the request." (line 55), add:

```markdown

If a relevant skill is not installed locally, you can discover and install one
from the configured remote sources: call `search_skills("<keywords>")` to list
matching skills (each result names its source), then call
`install_skill("<name>")` to install it. The user must approve the install;
once approved the skill's instructions are returned and can be followed
immediately, and the skill is available to `load_skill` in future sessions.
Configure sources via the `skill-sources` config key (a list of git `url` +
optional `path` entries). Only search/install skills when the request clearly
needs one that is not already present.
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/prompts -run TestIdentityCoversConfigKeys -count=1 -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/prompts/identity.md internal/prompts/prompts_test.go
git commit -m "docs(prompts): document skill-sources, search_skills, and install_skill"
```

---

## Final Verification

After all six tasks:

- [ ] **Run the full build + test suite**

```bash
go run github.com/go-task/task/v3/cmd/task@v3.51.1 check
go run github.com/go-task/task/v3/cmd/task@v3.51.1 test
```
Expected: both PASS. If `golangci-lint` is installed, also run `golangci-lint run ./...`.

- [ ] **Confirm no Go import cycles**: `internal/config` must NOT import `internal/tooling`; `internal/tools` imports `internal/skills` and `internal/approval` (already true).
