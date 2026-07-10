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
