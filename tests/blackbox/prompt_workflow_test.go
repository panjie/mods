//go:build integration

package blackbox_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/evals"
	"github.com/panjie/mods/internal/proto"
	"gopkg.in/yaml.v3"
)

// Config generation and isolation must be checked even when no real-provider
// key is present. This test creates no process and makes no provider request.
func TestHarnessPromptModes(t *testing.T) {
	previousProfile, previousBuildErr := selectedProfile, buildErr
	t.Cleanup(func() { selectedProfile, buildErr = previousProfile, previousBuildErr })
	selectedProfile = &providerProfile{name: "openai", keyEnv: "MODS_BLACKBOX_FIXTURE_KEY", defaultModel: "fixture-model"}
	buildErr = nil
	t.Setenv("MODS_BLACKBOX_FIXTURE_KEY", "synthetic-key")
	t.Setenv(modelOverrideEnv, "")
	forEachPromptMode(t, func(t *testing.T, minimal bool) {
		h := newRealAIHarnessWithPromptMode(t, true, true, "auto", minimal)
		for _, name := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME"} {
			var value string
			for _, entry := range h.env {
				if v, ok := strings.CutPrefix(entry, name+"="); ok {
					value = v
				}
			}
			if value == "" || !strings.HasPrefix(h.skillsDir, filepath.Dir(value)+string(filepath.Separator)) {
				t.Fatalf("%s and skill directory must use the isolated fixture root", name)
			}
			if name != "XDG_CONFIG_HOME" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(value, "mods", "mods.yml"))
			if err != nil {
				t.Fatal(err)
			}
			var cfg cfgpkg.Config
			if err := yaml.Unmarshal(data, &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Minimal != minimal || !cfg.Raw || cfg.Role != "" || cfg.ReviewMode != cfgpkg.ReviewAuto || !cfg.ShowTokenUsage {
				t.Fatalf("unexpected harness modes: minimal=%t raw=%t role=%q review=%s usage=%t", cfg.Minimal, cfg.Raw, cfg.Role, cfg.ReviewMode, cfg.ShowTokenUsage)
			}
			if len(cfg.SkillsDirs) != 1 || cfg.SkillsDirs[0] != h.skillsDir {
				t.Fatalf("skill directories escaped fixture: %v", cfg.SkillsDirs)
			}
		}
	})
}

func TestRealAIBlackBoxSimpleAggregation(t *testing.T) {
	forEachPromptMode(t, func(t *testing.T, minimal bool) {
		h := newRealAIHarnessWithPromptMode(t, true, true, "never", minimal)
		if !h.profile.filesystemTools {
			t.Skip("provider does not support tools")
		}
		const data = "value\n23\n17\n2\n"
		path := filepath.Join(h.cwd, "numbers.csv")
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, _ := h.run(t, "", "Sum the value column in numbers.csv using a short command or clear pipeline. Leave the fixture unchanged. Return only JSON with a total field.")
		var got struct {
			Total int `json:"total"`
		}
		decodeJSONObject(t, stdout, &got)
		if got.Total != 42 {
			t.Errorf("total=%d, want 42", got.Total)
		}
		if current, err := os.ReadFile(path); err != nil || string(current) != data {
			t.Errorf("input was modified: %v", err)
		}
		entries, err := os.ReadDir(h.cwd)
		if err != nil || len(entries) != 1 {
			t.Errorf("unexpected files in fixture: %v (error: %v)", entries, err)
		}
		logExecutionScore(t, h, evals.CaseSimpleAggregation)
	})
}

func TestRealAIBlackBoxInstalledSkill(t *testing.T) {
	// Catalog injection belongs to normal mode. Minimal deliberately omits it.
	h := newRealAIHarness(t, true)
	if !h.profile.filesystemTools {
		t.Skip("provider does not support tools")
	}
	dir := filepath.Join(h.skillsDir, "fixture-audit")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	const skill = "---\nname: fixture-audit\ndescription: Report the fixture marker when asked for a fixture audit.\n---\n\nRead marker.txt and return its value as the marker field of a JSON object. Do not modify files.\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o600); err != nil {
		t.Fatal(err)
	}
	const marker = "MAPLE-2841"
	if err := os.WriteFile(filepath.Join(h.cwd, "marker.txt"), []byte(marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, _ := h.run(t, "", "Use the fixture-audit skill to report the fixture marker. Return only JSON.")
	var got struct {
		Marker string `json:"marker"`
	}
	decodeJSONObject(t, stdout, &got)
	if got.Marker != marker {
		t.Errorf("marker=%q, want %q", got.Marker, marker)
	}
	loaded := false
	for _, message := range h.messages(t) {
		if message.Role != proto.RoleAssistant {
			continue
		}
		for _, call := range message.ToolCalls {
			if call.Function.Name == "load_skill" && strings.Contains(string(call.Function.Arguments), "fixture-audit") {
				loaded = true
			}
		}
	}
	if !loaded {
		t.Error("fixture-audit was not loaded")
	}
	logExecutionScore(t, h, evals.CaseInstalledSkill)
}
