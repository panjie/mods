package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/skills"
	"github.com/stretchr/testify/require"
)

// setupSkillFixture builds a temp skills-dir with two skills: one flat,
// one multi-file. Returns the dir and the parsed catalog.
func setupSkillFixture(t *testing.T) (string, []skills.Skill) {
	t.Helper()
	root := t.TempDir()
	// Flat skill.
	flatDir := filepath.Join(root, "flat", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(flatDir), 0o700))
	require.NoError(t, os.WriteFile(flatDir, []byte("---\nname: flat\ndescription: A flat skill.\n---\n\nFlat body.\n"), 0o600))
	// Multi-file skill with reference/ and scripts/.
	multiDir := filepath.Join(root, "multi")
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "reference"), 0o700))
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "scripts"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "SKILL.md"), []byte("---\nname: multi\ndescription: A multi-file skill.\n---\n\nSee reference/detail.md.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "reference", "detail.md"), []byte("Detail content.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "scripts", "run.py"), []byte("print('hi')\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	require.Len(t, catalog, 2)
	return root, catalog
}

func loadSkillTool(t *testing.T, catalog []skills.Skill) (*Registry, string) {
	t.Helper()
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, catalog))
	// Sanity: tool is registered.
	_, ok := reg.Tool("load_skill")
	require.True(t, ok)
	return reg, ""
}

func callLoadSkill(t *testing.T, reg *Registry, args string) string {
	t.Helper()
	out, err := reg.Call(context.Background(), "load_skill", []byte(args))
	require.NoError(t, err)
	return out
}

func TestLoadSkillBody(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"flat"}`)
	require.Equal(t, "Flat body.", got)
}

func TestLoadSkillBodyMultiFileSkill(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi"}`)
	require.Equal(t, "See reference/detail.md.", got)
}

func TestLoadSkillNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"nonexistent"}`)
	require.Equal(t, "skill not found: nonexistent. Available: flat, multi", got)
}

func TestLoadSkillNamePathEscapeIsOrdinaryNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	for _, bad := range []string{`..`, `../etc`, `/etc/passwd`, `a/b`} {
		got := callLoadSkill(t, reg, `{"name":"`+bad+`"}`)
		require.Contains(t, got, "skill not found", "input %q", bad)
	}
}

func TestLoadSkillAuxFile(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/detail.md"}`)
	require.Equal(t, "Detail content.\n", got)
}

func TestLoadSkillAuxFileUsesOverridingSkillDir(t *testing.T) {
	first, catalog1 := setupSkillFixture(t)
	second := t.TempDir()
	multiDir := filepath.Join(second, "multi")
	require.NoError(t, os.MkdirAll(filepath.Join(multiDir, "reference"), 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "SKILL.md"), []byte("---\nname: multi\ndescription: Override multi.\n---\n\nOverride instructions.\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "reference", "detail.md"), []byte("Override detail.\n"), 0o600))

	catalog, err := skills.ScanDirs([]string{first, second})
	require.NoError(t, err)
	require.NotEmpty(t, catalog1)

	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/detail.md"}`)
	require.Equal(t, "Override detail.\n", got)
}

func TestLoadSkillAuxFileScript(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"scripts/run.py"}`)
	require.Equal(t, "print('hi')\n", got)
}

func TestLoadSkillAuxFilePathEscapeRejected(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	for _, bad := range []string{`../etc/passwd`, `a/../../b`, `/etc/passwd`} {
		args := `{"name":"multi","file":"` + bad + `"}`
		got := callLoadSkill(t, reg, args)
		require.Contains(t, got, "invalid file path", "input %q", bad)
		require.NotContains(t, got, "Detail content", "must not read escaped path %q", bad)
	}
}

func TestLoadSkillAuxFileNonexistent(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"reference/missing.md"}`)
	require.Contains(t, got, "could not read file")
}

func TestLoadSkillAuxFileSkillNotFound(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"nonexistent","file":"reference/x.md"}`)
	require.Contains(t, got, "skill not found: nonexistent")
}

func TestLoadSkillEmptyCatalog(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, nil))
	got := callLoadSkill(t, reg, `{"name":"anything"}`)
	require.Contains(t, got, "skill not found: anything")
	require.Contains(t, got, "Available:") // empty list, but header present
}

func TestLoadSkillIdempotentBody(t *testing.T) {
	_, catalog := setupSkillFixture(t)
	reg, _ := loadSkillTool(t, catalog)
	first := callLoadSkill(t, reg, `{"name":"flat"}`)
	second := callLoadSkill(t, reg, `{"name":"flat"}`)
	require.Equal(t, first, second)
}

func TestRegisterSkillSpecHarvested(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterSkill(reg, nil))
	tool, ok := reg.Tool("load_skill")
	require.True(t, ok)
	require.Equal(t, "load_skill", tool.Spec.Name)
	require.NotEmpty(t, tool.Spec.Description)
	require.True(t, tool.Capabilities.ReadOnly)
	// Verify input schema has name (required) and file (optional).
	props, ok := tool.Spec.InputSchema["properties"].(map[string]any)
	require.True(t, ok)
	require.Contains(t, props, "name")
	require.Contains(t, props, "file")
	required, ok := tool.Spec.InputSchema["required"].([]string)
	require.True(t, ok)
	require.Equal(t, []string{"name"}, required)
}

func TestLoadSkillLargeFileRejected(t *testing.T) {
	root := t.TempDir()
	multiDir := filepath.Join(root, "multi", "scripts")
	require.NoError(t, os.MkdirAll(multiDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "multi", "SKILL.md"), []byte("---\nname: multi\ndescription: m.\n---\n\nbody.\n"), 0o600))
	// Write a file just over the 256 KB cap.
	big := strings.Repeat("x", SkillFileMaxBytes+1)
	require.NoError(t, os.WriteFile(filepath.Join(multiDir, "big.txt"), []byte(big), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	reg, _ := loadSkillTool(t, catalog)
	got := callLoadSkill(t, reg, `{"name":"multi","file":"scripts/big.txt"}`)
	require.Contains(t, got, "file too large")
}
