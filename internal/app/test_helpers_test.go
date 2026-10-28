package app

import (
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
