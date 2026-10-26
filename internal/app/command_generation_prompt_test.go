package app

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestReadOnlyGenerationGuidanceReachesNormalAndMinimal(t *testing.T) {
	for _, minimal := range []bool{false, true} {
		t.Run(map[bool]string{false: "normal", true: "minimal"}[minimal], func(t *testing.T) {
			cfg := defaultConfig()
			cfg.WorkingDir = t.TempDir()
			cfg.NoInstructions = true
			cfg.NoSave = true
			cfg.SkillsDirs = []string{filepath.Join(cfg.WorkingDir, "no-skills")}
			cfg.Minimal = minimal
			cfg.Role = ""
			cfg.Format = ""
			cfg.MCPServers = nil
			cfg.BuiltinTools.Filesystem = FilesystemAlways
			cfg.BuiltinTools.Shell = true
			m, err := New(context.Background(), &cfg, nil)
			require.NoError(t, err)
			t.Cleanup(m.ctxCancel)
			require.NoError(t, m.setupStreamContext("Inspect fixture files"))
			registry, err := m.buildToolRegistryForProvider(m.ctx, &cfg, websearch.Config{}, "Inspect fixture files", promptBudgetClient{tools: true})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, registry.Close()) })
			require.NoError(t, m.injectToolSelectionPrompt(registry))
			var system strings.Builder
			for _, message := range m.messages {
				if message.Role == proto.RoleSystem {
					system.WriteString(message.Content)
				}
			}
			require.Equal(t, !minimal, strings.Contains(system.String(), "Avoid incidental writes"))
			require.Equal(t, 1, strings.Count(system.String(), prompts.QueryPolicy))
			commandTools := 0
			for _, spec := range registry.Specs() {
				if registry.ShellExecution(spec.Name) {
					commandTools++
					require.Contains(t, spec.Description, prompts.ScriptPolicy)
					require.Contains(t, spec.Description, "read-only pipelines")
					require.Contains(t, spec.Description, "Group related reads, not stateful scripts")
					require.Contains(t, spec.Description, "Compute requested metrics in tools")
					require.Contains(t, spec.Description, "not from text or file sizes")
					require.Contains(t, spec.Description, "Avoid existence checks, suppressed errors")
					if spec.Name == "powershell_run" {
						require.Contains(t, spec.Description, prompts.PowerShellIntentGuidance)
						require.Contains(t, spec.Description, "Physical lines: Get-Content without -Raw | Measure-Object; use Count, never -Line")
					}
				}
			}
			require.Positive(t, commandTools)
		})
	}
}
