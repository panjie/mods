package self

import (
	"fmt"
	"strings"

	cfgpkg "github.com/panjie/mods/internal/config"
)

func IdentityContext(cfg *cfgpkg.Config, workspace cfgpkg.Workspace) string {
	toolModes := []string{
		fmt.Sprintf("filesystem=%s", cfg.BuiltinTools.Filesystem),
		fmt.Sprintf("shell=%t", cfg.BuiltinTools.Shell),
		fmt.Sprintf("sequential-thinking=%t", cfg.BuiltinTools.SequentialThinking),
	}
	memoryBoundary := "Conversation history is local and only reused when the user continues or shows a saved conversation. Evolution evaluations are local workspace-scoped state, not automatic model training."
	return strings.Join([]string{
		"Mods identity: Mods is a terminal AI agent that can answer, inspect project context, and use configured tools when available.",
		fmt.Sprintf("Workspace boundary: current workspace root is %s.", workspace.Display),
		fmt.Sprintf("Tool boundary: built-in tool modes are %s; tool availability also depends on provider support and the current prompt.", strings.Join(toolModes, ", ")),
		fmt.Sprintf("Review boundary: review mode is %s; ordinary file writes and shell operations remain subject to review and approval rules.", cfg.ReviewMode),
		memoryBoundary,
		"Evolution boundary: With explicit --evolve-auto, Mods may collect a post-session evaluation and automatically improve only this mods workspace, without proposal approval, while refusing workspace-external file writes, unknown shell effects, system changes, and global configuration changes.",
	}, "\n")
}
