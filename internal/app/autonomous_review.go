package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	toolregistry "github.com/panjie/mods/internal/tools"
)

func (m *Mods) authorizeAutonomousWorkspaceTool(registry *toolregistry.Registry, name string, data []byte) error {
	if registry == nil {
		return fmt.Errorf("automatic improvement refused %s: tool registry is unavailable", name)
	}
	if !registry.Mutable(name) {
		return nil
	}
	if registry.ShellExecution(name) {
		cmd := extractShellCommand(data)
		if strings.TrimSpace(cmd) == "" {
			return fmt.Errorf("automatic improvement refused %s: shell command is missing", name)
		}
		analysis := m.analyzeShellCommand(name, cmd)
		if !analysis.NeedsReview {
			return nil
		}
		if len(analysis.AffectedDirs) == 0 {
			return fmt.Errorf("automatic improvement refused %s: shell command affected directories are unknown", name)
		}
		if !m.allDirsWithinWorkspace(analysis.AffectedDirs) {
			return fmt.Errorf("automatic improvement refused %s: shell command affects paths outside the workspace", name)
		}
		return nil
	}
	switch name {
	case "fs_write_file":
		var parsed struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &parsed); err != nil || strings.TrimSpace(parsed.Path) == "" {
			return fmt.Errorf("automatic improvement refused %s: file path is missing", name)
		}
		if !m.pathWithinWorkspace(parsed.Path) {
			return fmt.Errorf("automatic improvement refused %s: file path is outside the workspace", name)
		}
		return nil
	case "fs_apply_patch":
		return nil
	default:
		return fmt.Errorf("automatic improvement refused mutable tool %s", name)
	}
}

func (m *Mods) allDirsWithinWorkspace(dirs []string) bool {
	for _, dir := range dirs {
		if !m.pathWithinWorkspace(dir) {
			return false
		}
	}
	return true
}

func (m *Mods) pathWithinWorkspace(path string) bool {
	workspace := m.Config.ResolveWorkspace()
	if strings.TrimSpace(workspace.Canonical) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	roots := []string{workspace.Canonical, workspace.Abs}
	target := path
	if !filepath.IsAbs(target) {
		target = filepath.Join(workspace.Canonical, target)
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	targetAbs = filepath.Clean(targetAbs)
	for _, root := range roots {
		root = filepath.Clean(root)
		rel, err := filepath.Rel(root, targetAbs)
		if err != nil {
			continue
		}
		if rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
			return true
		}
	}
	return false
}
