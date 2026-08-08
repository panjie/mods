package app

import (
	"runtime"
	"strings"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func renderToolSelectionPrompt(registry *toolregistry.Registry, goos string) string {
	if registry == nil || registry.Len() == 0 {
		return ""
	}
	hasFilesystem := false
	hasShell := false
	hasProcess := false
	for _, spec := range registry.Specs() {
		tool, _ := registry.Tool(spec.Name)
		if tool.Kind == toolregistry.ToolKindBuiltin && strings.HasPrefix(spec.Name, "fs_") {
			hasFilesystem = true
		}
		if registry.ShellExecution(spec.Name) {
			hasShell = true
		}
		if spec.Name == "process_run" {
			hasProcess = true
		}
	}
	if !hasFilesystem && !hasShell {
		return ""
	}

	parts := []string{prompts.ToolSelectionGeneral}
	if hasFilesystem {
		parts = append(parts, prompts.ToolSelectionFilesystem)
	}
	if hasProcess {
		parts = append(parts, prompts.ToolSelectionProcess)
	}
	if hasShell {
		if goos == "windows" {
			if hasProcess {
				parts = append(parts, prompts.ToolSelectionShellWindows)
			} else {
				parts = append(parts, prompts.ToolSelectionShellWindowsFallback)
			}
		} else if hasProcess {
			parts = append(parts, prompts.ToolSelectionShellPOSIX)
		} else {
			parts = append(parts, prompts.ToolSelectionShellPOSIXFallback)
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
	fallback := renderToolSelectionPrompt(registry, runtime.GOOS)
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
	msg := structuredSystemMessage(content, proto.SystemSectionExecutionTools)
	m.messages = append(m.messages, proto.Message{})
	copy(m.messages[insertAt+1:], m.messages[insertAt:])
	m.messages[insertAt] = msg
	debug.Printf("Prompt: injected tool-selection guidance (%d chars)", len(content))
	return nil
}
