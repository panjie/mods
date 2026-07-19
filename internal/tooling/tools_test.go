package tooling

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/selfhelp"
	"github.com/panjie/mods/internal/skills"
	toolregistry "github.com/panjie/mods/internal/tools"
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
		"fs_replace":    false,
		"mods_help":     false,
		"search_skills": false,
		"shell_run":     false,
		"web_search":    false,
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

	originalName := got[0].Name
	got[0].Name = "mutated"
	again, err := BuiltinSpecs()
	require.NoError(t, err)
	require.Equal(t, originalName, again[0].Name)
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
	cfg.SkillsDirs = []string{root}
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", catalog)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.True(t, ok, "load_skill must be registered when catalog is non-empty")
}

func TestBuildRegistrySkipsLoadSkillWhenCatalogEmpty(t *testing.T) {
	cfg := cfgpkg.Default()
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil)
	require.NoError(t, err)
	_, ok := reg.Tool("load_skill")
	require.False(t, ok, "load_skill must NOT be registered when catalog is empty")
	_, ok = reg.Tool("mods_help")
	require.True(t, ok, "mods_help must be registered independently of skills")
}

func TestBuildRegistryPassesSharedReferenceToModsHelp(t *testing.T) {
	cfg := cfgpkg.Default()
	reference := selfhelp.NewReference(selfhelp.Catalog{
		Settings: []selfhelp.Setting{{
			Path: "generated-setting", ValueType: "string", Description: "Generated description.",
		}},
	})
	reg, err := BuildRegistry(
		context.Background(),
		&cfg,
		websearch.Config{},
		"",
		nil,
		toolregistry.InteractionHandlers{SelfHelp: reference},
	)
	require.NoError(t, err)
	result, err := reg.Call(
		context.Background(),
		toolregistry.ModsHelpToolName,
		[]byte(`{"topic":"config"}`),
	)
	require.NoError(t, err)
	require.Contains(t, result, "`generated-setting`")
	require.Contains(t, result, "Generated description.")
}

func TestBuildRegistryFilesystemUsesApprovalSafeDirs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("/tmp is a POSIX safe directory")
	}

	probe, err := os.CreateTemp("/tmp", "mods-safe-dir-*.txt")
	require.NoError(t, err)
	target := probe.Name()
	require.NoError(t, probe.Close())
	require.NoError(t, os.Remove(target))
	t.Cleanup(func() { _ = os.Remove(target) })

	cfg := cfgpkg.Default()
	cfg.Plan = true // plan mode always registers filesystem tools
	reg, err := BuildRegistry(context.Background(), &cfg, websearch.Config{}, "", nil)
	require.NoError(t, err)

	args, err := json.Marshal(map[string]string{"path": target, "content": "safe-dir consistency"})
	require.NoError(t, err)
	_, err = reg.Call(context.Background(), "fs_write_file", args)
	require.NoError(t, err, "a path treated as approval-safe must also be accepted by the filesystem tool")

	got, err := os.ReadFile(target)
	require.NoError(t, err)
	require.Equal(t, "safe-dir consistency", string(got))
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

func TestBuiltinSpecsIncludesSkillDiscoveryButExcludesInstallTools(t *testing.T) {
	specs, err := BuiltinSpecs()
	require.NoError(t, err)
	have := map[string]bool{}
	for _, s := range specs {
		have[s.Name] = true
	}
	require.True(t, have["search_skills"], "search_skills must appear in --list-tools")
	require.False(t, have["install_skill"], "install_skill must not appear in --list-tools")
	require.False(t, have["thinking_note"], "removed thinking_note must not appear in --list-tools")
}
