package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

func TestScanDirsMergesAndLaterDirsOverride(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()
	writeSkill(t, first, "shared", "---\nname: shared\ndescription: Shared from first.\n---\n\nfirst body\n")
	writeSkill(t, first, "alpha", "---\nname: alpha\ndescription: Alpha skill.\n---\n\nalpha body\n")
	writeSkill(t, second, "shared", "---\nname: shared\ndescription: Shared from second.\n---\n\nsecond body\n")
	writeSkill(t, second, "beta", "---\nname: beta\ndescription: Beta skill.\n---\n\nbeta body\n")

	got, err := ScanDirs([]string{first, second})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.Equal(t, []string{"alpha", "beta", "shared"}, []string{got[0].Name, got[1].Name, got[2].Name})
	require.Equal(t, "Shared from second.", got[2].Description)
	require.Equal(t, filepath.Join(second, "shared"), got[2].Dir)
}

func TestScanDirsMissingDirsAreIgnored(t *testing.T) {
	got, err := ScanDirs([]string{filepath.Join(t.TempDir(), "missing")})
	require.NoError(t, err)
	require.Nil(t, got)
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

func TestCatalogPromptEmptyReturnsEmpty(t *testing.T) {
	require.Equal(t, "", CatalogPrompt(nil))
	require.Equal(t, "", CatalogPrompt([]Skill{}))
}

func TestCatalogPromptFormat(t *testing.T) {
	skills := []Skill{
		{Name: "bravo", Description: "Bravo skill."},
		{Name: "alpha", Description: "Alpha skill."},
	}
	got := CatalogPrompt(skills)
	require.Contains(t, got, "## Available skills")
	require.Contains(t, got, "load_skill")
	require.Contains(t, got, "search_skills")
	require.Contains(t, got, "- alpha: Alpha skill.")
	require.Contains(t, got, "- bravo: Bravo skill.")
	// Rendering is stable even when the caller's catalog is not sorted.
	require.Less(t, strings.Index(got, "alpha"), strings.Index(got, "bravo"))
}

func TestCatalogPromptBoundsDescriptionsAndTotal(t *testing.T) {
	catalog := make([]Skill, 40)
	for i := range catalog {
		catalog[i] = Skill{
			Name:        fmt.Sprintf("skill-%02d", i),
			Description: strings.Repeat("你", 200),
		}
	}
	got := CatalogPrompt(catalog)
	require.LessOrEqual(t, len(got), MaxCatalogBytes)
	require.True(t, utf8.ValidString(got))
	require.Contains(t, got, "omitted")

	render := CatalogPromptBudget(catalog[:1], MaxCatalogBytes)
	line := strings.TrimPrefix(strings.Split(render.Prompt, "\n")[3], "- skill-00: ")
	require.LessOrEqual(t, len(line), MaxDescriptionBytes)
}

func TestCatalogPromptBudgetCanOmitEntireCatalog(t *testing.T) {
	render := CatalogPromptBudget([]Skill{{Name: "alpha", Description: "desc"}}, 8)
	require.Empty(t, render.Prompt)
	require.Equal(t, 1, render.Omitted)
}
