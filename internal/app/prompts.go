package app

import (
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
)

func structuredSystemMessage(content string, section proto.SystemSection) proto.Message {
	message := proto.Message{Role: proto.RoleSystem, Content: content}
	message.SetSystemSection(section)
	return message
}

func (m *Mods) resolvePrompt(key, fallback string) (string, error) {
	configured := ""
	if m.Config != nil {
		configured = m.Config.Prompts.Value(key)
	}
	if strings.TrimSpace(configured) == "" {
		return fallback, nil
	}
	content, err := loadMsg(m.ctx, configured)
	if err != nil {
		return "", modsError{
			Err:        err,
			ReasonText: fmt.Sprintf("Could not use prompt %q", key),
		}
	}
	debug.Printf("Prompt override: %s (%d chars)", key, len(content))
	return content, nil
}

func formatTemporaryDirectoryPrompt(path string) string {
	return strings.ReplaceAll(prompts.TemporaryDirectoryTemplate, "{temporary_directory}", path)
}

// interactiveInputAvailable reports whether mods can prompt for secrets in this
// terminal. Sudo askpass and Git credential prompts share that capability, so
// their guidance is injected only when it describes a reachable flow.
func (m *Mods) interactiveInputAvailable() bool {
	return m.userInput != nil && m.userInput.available()
}
