package app

import (
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/prompts"
)

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

func formatSafeWorkspacePrompt(path string) string {
	return strings.ReplaceAll(prompts.SafeWorkspaceTemplate, "{safe_workspace}", path)
}

func formatApprovedPlanPrompt(plan string) string {
	return strings.ReplaceAll(prompts.ApprovedPlanTemplate, "{approved_plan}", plan)
}
