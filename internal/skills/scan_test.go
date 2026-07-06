package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeSkill writes a SKILL.md inside dir/<name>/ with the given content.
func writeSkill(t *testing.T, dir, name, content string) {
	t.Helper()
	skillDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(skillDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o600))
}

func TestScanNormal(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "alpha", "---\nname: alpha\ndescription: Alpha skill.\n---\n\nBody text.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "alpha", skills[0].Name)
	require.Equal(t, "Alpha skill.", skills[0].Description)
	require.Equal(t, "Body text.", skills[0].Body)
	require.Contains(t, skills[0].Dir, "alpha")
}

func TestScanMissingNameFallsBackToDirName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "beta", "---\ndescription: Beta skill.\n---\n\nBody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "beta", skills[0].Name)
	require.Equal(t, "Beta skill.", skills[0].Description)
}

func TestScanMissingDescriptionUsesPlaceholder(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "gamma", "---\nname: gamma\n---\n\nBody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "gamma", skills[0].Name)
	require.Equal(t, "(no description)", skills[0].Description)
}

func TestScanNoFrontmatterWholeFileIsBody(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "delta", "# Delta\n\nJust a body, no frontmatter.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "delta", skills[0].Name)
	require.Equal(t, "(no description)", skills[0].Description)
	require.Contains(t, skills[0].Body, "Just a body")
}

func TestScanUnknownFrontmatterFieldsIgnored(t *testing.T) {
	root := t.TempDir()
	content := "---\nname: epsilon\ndescription: Epsilon.\nlicense: MIT\nrequires:\n  mcp: [rube]\n---\nBody.\n"
	writeSkill(t, root, "epsilon", content)
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "epsilon", skills[0].Name)
	require.Equal(t, "Epsilon.", skills[0].Description)
	require.Equal(t, "Body.", skills[0].Body)
}

func TestScanUnterminatedFrontmatter(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "zeta", "---\nname: zeta\n\ndescription: never closed\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "zeta", skills[0].Name)          // falls back to dir name
	require.Contains(t, skills[0].Body, "name: zeta") // whole file is body
}

func TestScanNameCollisionLaterWins(t *testing.T) {
	root := t.TempDir()
	// Two directories whose frontmatter name resolves to the same value.
	writeSkill(t, root, "dir-a", "---\nname: same\n---\n\nBody A.\n")
	writeSkill(t, root, "dir-b", "---\nname: same\n---\n\nBody B.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	// Directory walk order is lexical, so dir-b is processed last and wins.
	require.Equal(t, "Body B.", skills[0].Body)
}

func TestScanSortedByName(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, "charlie", "---\nname: charlie\n---\n\nc.\n")
	writeSkill(t, root, "alpha", "---\nname: alpha\n---\n\na.\n")
	writeSkill(t, root, "bravo", "---\nname: bravo\n---\n\nb.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 3)
	require.Equal(t, "alpha", skills[0].Name)
	require.Equal(t, "bravo", skills[1].Name)
	require.Equal(t, "charlie", skills[2].Name)
}

func TestScanEmptyDirReturnsNilNoError(t *testing.T) {
	root := t.TempDir()
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Nil(t, skills)
}

func TestScanNonexistentDirReturnsNilNoError(t *testing.T) {
	skills, err := Scan(filepath.Join(t.TempDir(), "does-not-exist"))
	require.NoError(t, err)
	require.Nil(t, skills)
}

func TestScanSkipsDirsLackingSkillMd(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "no-skill"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "no-skill", "README.md"), []byte("hi"), 0o600))
	writeSkill(t, root, "real", "---\nname: real\n---\n\nbody.\n")
	skills, err := Scan(root)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "real", skills[0].Name)
}
