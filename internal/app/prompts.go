package app

import (
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
)

const maxSkillSafeDirsPrompt = 20

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

func formatSafeWorkspacePrompt(path string, skillDirs []string) string {
	content := strings.ReplaceAll(prompts.SafeWorkspaceTemplate, "{safe_workspace}", path)
	if len(skillDirs) == 0 {
		return content
	}
	var sb strings.Builder
	sb.WriteString(content)
	sb.WriteString("\nSkill safe directories: File write and shell operations within these loaded skill directories and their subdirectories are also auto-approved without user review in normal review modes. Review mode always still prompts.")
	limit := len(skillDirs)
	if limit > maxSkillSafeDirsPrompt {
		limit = maxSkillSafeDirsPrompt
	}
	for _, dir := range skillDirs[:limit] {
		sb.WriteString("\n- ")
		sb.WriteString(dir)
	}
	if omitted := len(skillDirs) - limit; omitted > 0 {
		sb.WriteString(fmt.Sprintf("\n- ... %d more omitted", omitted))
	}
	return sb.String()
}

func formatApprovedPlanPrompt(plan string) string {
	return strings.ReplaceAll(prompts.ApprovedPlanTemplate, "{approved_plan}", plan)
}
