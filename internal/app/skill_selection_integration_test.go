//go:build integration

package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/skills"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

// Exercise production prompt/tool assembly, but never execute tools. Synthetic
// installed skills keep this evaluation independent of the host's skill files.
func TestDeepSeekSkillSelectionFirstRoundIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "skill selection")
	for _, scenario := range []struct {
		name, prompt string
		wantSkill    bool
	}{
		{"read-only-lock-screen", "这台电脑运行 Omarchy。目前系统多久进入锁屏状态？", true},
		{"unrelated", "17 加 25 等于多少？", false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
			root := t.TempDir()
			skillDir := filepath.Join(root, "skills", "omarchy")
			require.NoError(t, os.MkdirAll(skillDir, 0o700))
			require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(`---
name: omarchy
description: >
  REQUIRED for end-user customization of Linux desktop, window manager, or system config.
  Use when editing ~/.config/hypr/, ~/.config/omarchy/, ~/.config/alacritty/,
  ~/.config/foot/, ~/.config/kitty/, or ~/.config/ghostty/.
  Triggers: Hyprland, window rules, animations, keybindings, monitors, gaps, borders,
  blur, opacity, omarchy-shell, bar, terminal config, themes, background,
  night light, idle, lock screen, screenshots, reminders, layer rules, workspace settings.
---
Read the relevant configuration before answering questions about idle and lock screen behavior.
`), 0o600))
			m.Config.WorkingDir, m.Config.SkillsDirs = root, []string{filepath.Dir(skillDir)}
			var err error
			m.skillCatalog, err = skills.ScanCatalogDirs(m.Config.SkillsDirs)
			require.NoError(t, err)
			m.Config.NoSave, m.Config.Raw, m.Config.NoInstructions = true, true, true
			m.Config.Minimal, m.Config.Format = false, ""
			m.Config.BuiltinTools.Filesystem, m.Config.BuiltinTools.Shell = FilesystemAlways, true
			require.NoError(t, m.setupStreamContext(scenario.prompt))
			api, model, err := m.resolveModel(m.Config)
			require.NoError(t, err)
			cfgs, err := m.buildProviderConfigs(model, api)
			require.NoError(t, err)
			_, err = m.resolveThinkWithOllama(&model, &cfgs.Anthropic, &cfgs.Google, &cfgs.Ollama, &cfgs.OpenAI)
			require.NoError(t, err)
			client, err := newStreamClient(modelProtocol(model), cfgs.Anthropic, cfgs.Google, cfgs.Ollama, cfgs.OpenAI)
			require.NoError(t, err)
			registry, err := m.buildToolRegistryForProvider(m.ctx, m.Config, websearch.Config{}, scenario.prompt, client)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, registry.Close()) })
			require.NoError(t, m.injectToolSelectionPrompt(registry))
			ctx, cancel := context.WithTimeout(m.ctx, time.Minute)
			defer cancel()
			maxTokens := int64(1024)
			st := client.Request(ctx, proto.Request{
				Messages: m.messages, API: model.API, Model: model.Name, Tools: registry.Specs(),
				Temperature: ptrOrNil(float64(0)), MaxTokens: &maxTokens, TrackUsage: true,
			})
			defer func() { _ = st.Close() }()
			for st.Next() {
				chunk, err := st.Current()
				if err != nil && !errors.Is(err, stream.ErrNoContent) {
					require.NoError(t, err)
				}
				if chunk.Content != "" {
					t.Logf("answer: %s", chunk.Content)
				}
			}
			require.NoError(t, st.Err())
			var calls []proto.ToolCall
			for _, msg := range st.Messages() {
				if msg.Role == proto.RoleAssistant {
					calls = append(calls, msg.ToolCalls...)
				}
			}
			t.Logf("model=%s first_round_only=true tools_executed=false calls=%d", model.Name, len(calls))
			if !scenario.wantSkill {
				for _, call := range calls {
					require.NotEqual(t, "load_skill", call.Function.Name)
					require.NotEqual(t, "search_skills", call.Function.Name)
				}
				return
			}
			require.Len(t, calls, 1, "load the applicable skill before requesting any investigation")
			require.Equal(t, "load_skill", calls[0].Function.Name)
			var args struct {
				Name string `json:"name"`
				File string `json:"file"`
			}
			require.NoError(t, json.Unmarshal(calls[0].Function.Arguments, &args))
			require.Equal(t, "omarchy", args.Name)
			require.Empty(t, args.File, "load the skill instructions first")
		})
	}
}
