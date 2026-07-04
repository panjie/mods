package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// maxInstructionsBytes caps how much of AGENTS.md is injected as project
// context, keeping per-turn token cost bounded for very large files.
const maxInstructionsBytes = 16 * 1024

// loadProjectInstructions reads AGENTS.md from the workspace root to inject as
// project context. It returns "" when disabled (cfg.NoInstructions), in
// minimal mode, when no workspace is configured, or when AGENTS.md is absent
// (the common case). A missing file is not an error: most workspaces have no
// AGENTS.md and the model simply runs without project-specific guidance.
func loadProjectInstructions(cfg *Config) string {
	if cfg == nil || cfg.NoInstructions || cfg.Minimal {
		return ""
	}
	root := cfg.ResolveWorkspace().Canonical
	if root == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(root, "AGENTS.md"))
	if err != nil {
		return ""
	}
	content := strings.TrimRight(string(data), "\r\n")
	if len(content) > maxInstructionsBytes {
		omitted := len(content) - maxInstructionsBytes
		content = content[:maxInstructionsBytes] +
			fmt.Sprintf("\n\n[... AGENTS.md truncated: %d more bytes omitted ...]", omitted)
	}
	return content
}
