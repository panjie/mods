package tooling

import (
	"sort"
	"testing"
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
