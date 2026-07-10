package tooling

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestBuiltinSpecs(t *testing.T) {
	got, err := BuiltinSpecs()
	if err != nil {
		t.Fatalf("BuiltinSpecs: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("BuiltinSpecs returned no tools")
	}
	// Cross-platform builtins that must always be present (powershell_run is
	// Windows-only and intentionally not asserted here).
	want := map[string]bool{
		"fs_read_file":  false,
		"fs_write_file": false,
		"shell_run":     false,
		"web_search":    false,
		"thinking_note": false,
	}
	seen := map[string]bool{}
	for _, info := range got {
		if info.Name == "" {
			t.Errorf("result contains a tool with an empty name")
			continue
		}
		if seen[info.Name] {
			t.Errorf("duplicate tool name: %s", info.Name)
		}
		seen[info.Name] = true
		if _, ok := want[info.Name]; ok {
			want[info.Name] = true
		}
		if info.Kind == "" {
			t.Errorf("tool %q has empty Kind", info.Name)
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("expected builtin tool %q not listed", name)
		}
	}
	names := make([]string, 0, len(got))
	for _, info := range got {
		names = append(names, info.Name)
	}
	sort.Strings(names)
	t.Logf("builtins (%d): %v", len(got), names)
}

func TestBuildRegistryRegistersLoadSkillWhenCatalogNonEmpty(t *testing.T) {
	root := t.TempDir()
	skillPath := filepath.Join(root, "demo", "SKILL.md")
	require.NoError(t, os.MkdirAll(filepath.Dir(skillPath), 0o700))
	require.NoError(t, os.WriteFile(skillPath, []byte("---\nname: demo\ndescription: Demo.\n---\n\nbody.\n"), 0o600))
	catalog, err := skills.Scan(root)
	require.NoError(t, err)
	require.Len(t, catalog, 1)

	cfg := cfgpkg.Default()
	cfg.SkillsDir = root
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", catalog, nil)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.True(t, ok, "load_skill must be registered when catalog is non-empty")
}

func TestBuildRegistrySkipsLoadSkillWhenCatalogEmpty(t *testing.T) {
	cfg := cfgpkg.Default()
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil, nil)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.False(t, ok, "load_skill must NOT be registered when catalog is empty")
}

func TestBuiltinSpecsIncludesLoadSkill(t *testing.T) {
	specs, err := BuiltinSpecs()
	require.NoError(t, err)
	found := false
	for _, s := range specs {
		if s.Name == "load_skill" {
			found = true
			require.True(t, s.ReadOnly, "load_skill must be ReadOnly")
		}
	}
	require.True(t, found, "load_skill must appear in --list-tools output")
}

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
