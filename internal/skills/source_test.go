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
		"git@github.com:obra/superpowers.git":     "github.com-obra-superpowers",
		"https://example.com/a/b.git":             "example.com-a-b",
		"https://example.com/a/b/":                "example.com-a-b",
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
		{Name: "alpha", Description: "mentions test here"}, // description hit only
		{Name: "testing", Description: "no keyword here"},  // name hit → ranks first
	}
	got := Search(cat, "test", 10)
	require.Len(t, got, 2)
	require.Equal(t, "testing", got[0].Name) // name hit ranks before description-only
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

func TestInstallOverwritesCorruptDestination(t *testing.T) {
	srcRoot := t.TempDir()
	writeSkill(t, srcRoot, "my", "---\nname: my\ndescription: My.\n---\n\nMy body.\n")
	skillsDir := t.TempDir()
	// Simulate a leftover partial/corrupt destination (dir exists, no SKILL.md).
	corrupt := filepath.Join(skillsDir, "my")
	require.NoError(t, os.MkdirAll(corrupt, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(corrupt, "junk"), []byte("partial"), 0o600))

	match := SourceSkill{Name: "my", Dir: filepath.Join(srcRoot, "my")}
	skill, err := Install(match, skillsDir)
	require.NoError(t, err)
	require.Equal(t, "My body.", skill.Body)
	// A proper install landed; the corrupt partial state is gone.
	require.FileExists(t, filepath.Join(skillsDir, "my", "SKILL.md"))
}
