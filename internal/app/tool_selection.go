package app

import (
	"runtime"
	"strings"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func renderToolSelectionPrompt(registry *toolregistry.Registry, plan bool, goos string) string {
	if registry == nil || registry.Len() == 0 {
		return ""
	}
	hasFilesystem := false
	hasShell := false
	for _, spec := range registry.Specs() {
		tool, _ := registry.Tool(spec.Name)
		if tool.Kind == toolregistry.ToolKindBuiltin && strings.HasPrefix(spec.Name, "fs_") {
			hasFilesystem = true
		}
		if registry.ShellExecution(spec.Name) {
			hasShell = true
		}
	}
	if !hasFilesystem && !hasShell {
		return ""
	}

	parts := make([]string, 0, 3)
	if plan {
		parts = append(parts, prompts.ToolSelectionPlanGeneral)
		if hasFilesystem {
			parts = append(parts, prompts.ToolSelectionPlanFilesystem)
		}
		if hasShell {
			if goos == "windows" {
				parts = append(parts, prompts.ToolSelectionPlanShellWindows)
			} else {
				parts = append(parts, prompts.ToolSelectionPlanShellPOSIX)
			}
		}
	} else {
		parts = append(parts, prompts.ToolSelectionGeneral)
		if hasFilesystem {
			parts = append(parts, prompts.ToolSelectionFilesystem)
		}
		if hasShell {
			if goos == "windows" {
				parts = append(parts, prompts.ToolSelectionShellWindows)
			} else {
				parts = append(parts, prompts.ToolSelectionShellPOSIX)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func (m *Mods) injectToolSelectionPrompt(registry *toolregistry.Registry) error {
	insertAt := m.toolSelectionInsertAt
	m.toolSelectionInsertAt = -1
	if m.Config == nil || m.Config.Minimal || registry == nil || registry.Len() == 0 {
		return nil
	}

	configured := strings.TrimSpace(m.Config.Prompts.ToolSelection) != ""
	fallback := renderToolSelectionPrompt(registry, m.Config.Plan, runtime.GOOS)
	if !configured && fallback == "" {
		return nil
	}
	content, err := m.resolvePrompt(prompts.KeyToolSelection, fallback)
	if err != nil {
		return err
	}

	if insertAt < 0 || insertAt > len(m.messages) {
		insertAt = 0
		for insertAt < len(m.messages) && m.messages[insertAt].Role == proto.RoleSystem {
			insertAt++
		}
	}
	msg := proto.Message{Role: proto.RoleSystem, Content: content}
	m.messages = append(m.messages, proto.Message{})
	copy(m.messages[insertAt+1:], m.messages[insertAt:])
	m.messages[insertAt] = msg
	debug.Printf("Prompt: injected tool-selection guidance (%d chars)", len(content))
	return nil
}
