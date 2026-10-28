package tools

import (
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/prompts"
	"github.com/stretchr/testify/require"
)

func TestCommandToolsDeclareEffectInSchemaAndGuidance(t *testing.T) {
	r := NewRegistry()
	root := t.TempDir()
	require.NoError(t, RegisterShell(r, ShellConfig{Root: root}))
	require.NoError(t, RegisterPowerShell(r, ShellConfig{Root: root}))
	require.NoError(t, RegisterProcess(r, ProcessConfig{Root: root}))
	for _, name := range []string{"shell_run", "powershell_run", "process_run"} {
		t.Run(name, func(t *testing.T) {
			tool, ok := r.Tool(name)
			require.True(t, ok)
			require.Contains(t, tool.Spec.InputSchema["required"], "effect")
			properties := tool.Spec.InputSchema["properties"].(map[string]any)
			effect := properties["effect"].(map[string]any)
			require.Equal(t, []string{"read_only", "may_write"}, effect["enum"])
			require.Contains(t, tool.Spec.Description, prompts.ScriptPolicy)
			require.Contains(t, tool.Spec.Description, CommandEffectGuidance)
			for _, field := range []string{``, `,"effect":null`, `,"effect":true`, `,"effect":"read"`, `,"effect":[]`} {
				raw := `{"command":"echo hi","program":"example","args":[]` + field + `}`
				require.ErrorContains(t, r.ValidateRequiredArgs(name, []byte(raw)), "effect", raw)
			}
			require.Error(t, r.ValidateRequiredArgs(name, []byte(`not json`)))
			for _, declared := range []string{"read_only", "may_write"} {
				data, err := json.Marshal(map[string]any{"command": "echo hi", "program": "example", "args": []string{}, "effect": declared})
				require.NoError(t, err)
				require.NoError(t, r.ValidateRequiredArgs(name, data))
			}
		})
	}
}
