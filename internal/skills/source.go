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
