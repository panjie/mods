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
	out, err := exec.CommandContext(ctx, "git", "clone", "--depth", "1", "--", url, dir).CombinedOutput()
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
		if existing, perr := parseSkillFile(dest); perr == nil {
			return existing, nil // idempotent: real, complete prior install
		}
		// dest exists but is corrupt/partial — fall through to a clean reinstall.
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
		return Skill{}, fmt.Errorf("could not parse SKILL.md in %q: %w", skillDir, err)
	}
	return skill, nil
}

func copyDir(src, dst string) error {
	parent := filepath.Dir(dst)
	base := filepath.Base(dst)
	tmp, err := os.MkdirTemp(parent, "."+base+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	walkErr := filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(tmp, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(target, data, info.Mode())
	})
	if walkErr != nil {
		cleanup()
		return walkErr
	}
	// Replace any pre-existing destination (e.g. corrupt partial state) only
	// AFTER the walk fully succeeded, so a copy failure never disturbs dst.
	if _, statErr := os.Stat(dst); statErr == nil {
		if rmErr := os.RemoveAll(dst); rmErr != nil {
			cleanup()
			return fmt.Errorf("remove existing destination: %w", rmErr)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		cleanup()
		return fmt.Errorf("rename into place: %w", err)
	}
	return nil
}
