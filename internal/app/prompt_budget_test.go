package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

type promptBudgetClient struct{ tools bool }

func (promptBudgetClient) Request(context.Context, proto.Request) stream.Stream { return nil }
func (c promptBudgetClient) Capabilities() stream.Capabilities {
	return stream.Capabilities{Tools: c.tools}
}

// These are fixture budgets, not runtime limits on user prompts, installed
// skills, projects or MCP servers. Use the real assembly and registration paths
// so moving guidance into descriptions or schemas cannot evade the budget.
func TestAssembledPromptBudgets(t *testing.T) {
	t.Setenv("GLAMOUR_STYLE", "")
	oldTTY := IsInputTTY
	t.Cleanup(func() { IsInputTTY = oldTTY })
	for _, tc := range []struct {
		name                                        string
		filesystem, shell, interactive, minimal     bool
		context, noTools                            bool
		system, descriptions, schemas, tools, total int
	}{
		{name: "normal/base", system: 5, descriptions: 1, schemas: 1, tools: 2, total: 7},
		{name: "normal/filesystem", filesystem: true, system: 6, descriptions: 6, schemas: 10, tools: 16, total: 22},
		// Command schemas include the user-facing purpose; keep the overall budget.
		{name: "normal/shell", shell: true, system: 6, descriptions: 5, schemas: 5, tools: 10, total: 15},
		{name: "normal/both", filesystem: true, shell: true, system: 6, descriptions: 10, schemas: 12, tools: 22, total: 28},
		{name: "normal/interactive", filesystem: true, shell: true, interactive: true, system: 7, descriptions: 13, schemas: 19, tools: 32, total: 39},
		{name: "normal/project-role-skill", filesystem: true, shell: true, context: true, system: 8, descriptions: 11, schemas: 13, tools: 24, total: 32},
		{name: "minimal/both", filesystem: true, shell: true, minimal: true, system: 2, descriptions: 10, schemas: 12, tools: 22, total: 24},
		{name: "normal/no-tool-provider", noTools: true, system: 5, total: 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			IsInputTTY = func() bool { return tc.interactive }
			root := t.TempDir()
			cfg := defaultConfig()
			cfg.WorkingDir, cfg.SkillsDirs = root, []string{filepath.Join(root, "skills")}
			cfg.NoSave, cfg.NoInstructions = true, !tc.context
			cfg.Minimal, cfg.Role, cfg.Format = tc.minimal, "", ""
			cfg.WebSearch, cfg.MCPServers = false, nil
			cfg.BuiltinTools.Filesystem, cfg.BuiltinTools.Shell = FilesystemNever, tc.shell
			if tc.filesystem {
				cfg.BuiltinTools.Filesystem = FilesystemAlways
			}
			if tc.context {
				require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Keep fixture files unchanged unless requested."), 0o600))
				cfg.Role, cfg.Roles = "reviewer", map[string][]string{"reviewer": {"Explain concrete findings briefly."}}
				path := filepath.Join(cfg.SkillsDirs[0], "demo", "SKILL.md")
				require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
				require.NoError(t, os.WriteFile(path, []byte("---\nname: demo\ndescription: Inspect a fixture when requested.\n---\n\nInspect the fixture.\n"), 0o600))
			}
			m, err := New(context.Background(), &cfg, nil)
			require.NoError(t, err)
			t.Cleanup(m.ctxCancel)
			require.NoError(t, m.setupStreamContext("hello"))
			registry, err := m.buildToolRegistryForProvider(m.ctx, &cfg, websearch.Config{}, "hello", promptBudgetClient{tools: !tc.noTools})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, registry.Close()) })
			require.NoError(t, m.injectToolSelectionPrompt(registry))
			m.injectSelfHelpFallback()
			size, err := proto.MeasurePromptSize(m.messages, registry.Specs())
			require.NoError(t, err)
			encoded, err := json.Marshal(size)
			require.NoError(t, err)
			t.Logf("prompt bytes (provider-neutral, not tokens): total=%d %s", size.TotalBytes(), encoded)
			require.LessOrEqual(t, size.SystemBytes, tc.system*1024, "rendered system")
			require.LessOrEqual(t, size.ToolDescriptionBytes, tc.descriptions*1024, "tool descriptions")
			require.LessOrEqual(t, size.ToolSchemaBytes, tc.schemas*1024, "schema JSON, including property descriptions")
			require.LessOrEqual(t, size.ToolDefinitionsBytes, tc.tools*1024, "complete tool definitions JSON")
			require.LessOrEqual(t, size.TotalBytes(), tc.total*1024, "system plus complete tool definitions")
			if tc.context {
				sections := map[proto.SystemSection]bool{}
				for _, section := range size.Sections {
					sections[section.Section] = true
				}
				for _, section := range []proto.SystemSection{proto.SystemSectionProjectInstructions, proto.SystemSectionUserRole, proto.SystemSectionExecutionSkills} {
					require.True(t, sections[section], "fixture must exercise section %d", section)
				}
			}
		})
	}
}
