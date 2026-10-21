package app

import (
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
)

// Test adapters keep older fixtures concise while exercising production entry points.
func (m *Mods) toolCaller(registry *toolregistry.Registry, cfg *Config) proto.ToolCaller {
	caller := m.toolResultCaller(registry, cfg)
	return func(call proto.ToolCallRequest) (string, error) {
		result, err := caller(call)
		return result.Text, err
	}
}

func (m *Mods) assessCommand(tool, command string) approval.CommandAssessment {
	return m.assessCommandWithEnv(tool, command, nil)
}

// assessCommandWithEnv additionally receives the environment names this
// call injects as secrets; static environment path expansion skips them so
// classification never substitutes a value the child shell will not see.
func (m *Mods) assessCommandWithEnv(tool, command string, shadowedEnv map[string]bool) approval.CommandAssessment {
	ws := ""
	if m.Config != nil {
		ws = m.Config.ResolveWorkingDir().Canonical
	}
	return m.assessCommandAtCwd(tool, command, shadowedEnv, ws)
}
