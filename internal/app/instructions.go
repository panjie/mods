package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/textutil"
	"github.com/panjie/mods/internal/ui"
)

// maxInstructionsBytes caps how much of AGENTS.md is injected as project
// context, keeping per-turn token cost bounded for very large files.
const maxInstructionsBytes = 16 * 1024

// loadProjectInstructions reads AGENTS.md from the cwd root to inject as
// project context. It returns "" when disabled (cfg.NoInstructions), in
// minimal mode, when no cwd is configured, or when AGENTS.md is absent
// (the common case). A missing file is not an error: most cwds have no
// AGENTS.md and the model simply runs without project-specific guidance.
func loadProjectInstructions(cfg *Config) string {
	content, _ := readProjectInstructions(cfg)
	return content
}

// readProjectInstructions returns the trimmed AGENTS.md content and its path.
// An empty content (and path) means nothing was loaded, so callers can use the
// path to announce a real load without repeating the gating rules.
func readProjectInstructions(cfg *Config) (string, string) {
	if cfg == nil || cfg.NoInstructions || cfg.Minimal {
		return "", ""
	}
	root := cfg.ResolveWorkingDir().Canonical
	if root == "" {
		return "", ""
	}
	path := filepath.Join(root, "AGENTS.md")
	data, err := os.ReadFile(path)
	if err != nil {
		return "", ""
	}
	content := strings.TrimRight(string(data), "\r\n")
	if len(content) > maxInstructionsBytes {
		omitted := len(content) - maxInstructionsBytes
		content = textutil.TruncateUTF8Bytes(content, maxInstructionsBytes) +
			fmt.Sprintf("\n\n[... AGENTS.md truncated: %d more bytes omitted ...]", omitted)
	}
	return content, path
}

// projectInstructionsNoticeLine renders the one-time record that announces
// AGENTS.md was loaded as project context, mirroring the tool-result records
// shown for load_skill. It returns "" when the notice is hidden or when the
// file contributes no instructions.
func (m *Mods) projectInstructionsNoticeLine() string {
	cfg := m.Config
	if cfg == nil || cfg.ProjectInstructionsNoticeShown {
		return ""
	}
	if !m.showOperationStatus || !cfg.InteractiveTTYAvailable || cfg.Minimal || cfg.NoInstructions {
		return ""
	}
	content, path := readProjectInstructions(cfg)
	if content == "" || path == "" {
		return ""
	}
	status := ui.TruncateOperationStatus("Project instructions: "+path, m.toolResultStatusWidth())
	return m.toolResultDisplayLine("✓ " + status)
}

// emitProjectInstructionsNotice returns the command that prints the AGENTS.md
// load record above the program, or nil when the notice is hidden or already
// shown in this process. The command is unmanaged by the program, so a retry
// or a later chat turn does not duplicate or erase the line.
func (m *Mods) emitProjectInstructionsNotice() tea.Cmd {
	line := m.projectInstructionsNoticeLine()
	if line == "" {
		return nil
	}
	m.Config.ProjectInstructionsNoticeShown = true
	return tea.Println(line)
}
