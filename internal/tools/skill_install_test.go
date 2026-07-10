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
