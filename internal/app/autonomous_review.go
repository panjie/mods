package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	toolregistry "github.com/panjie/mods/internal/tools"
)

// selfLayerRel is the only directory auto-evolution may modify. The trusted
// kernel enforces this boundary: automatic improvement may edit files and run
// mutable shell commands solely inside this directory. Everything outside it
// (the kernel, providers, DB schema) is off-limits to self-evolution.
const selfLayerRel = "internal/self"

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
		if !m.allDirsSelfLayer(analysis.AffectedDirs) {
			return fmt.Errorf("automatic improvement refused %s: shell command affects paths outside the self layer", name)
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
		if !m.pathWithinSelf(parsed.Path) {
			return fmt.Errorf("automatic improvement refused %s: file path is outside the self layer", name)
		}
		return nil
	case "fs_apply_patch":
		paths, err := extractPatchPaths(data)
		if err != nil {
			return fmt.Errorf("automatic improvement refused %s: %w", name, err)
		}
		for _, p := range paths {
			if !m.pathWithinSelf(p) {
				return fmt.Errorf("automatic improvement refused %s: patch path %q is outside the self layer", name, p)
			}
		}
		return nil
	default:
		return fmt.Errorf("automatic improvement refused mutable tool %s", name)
	}
}

func (m *Mods) allDirsSelfLayer(dirs []string) bool {
	for _, dir := range dirs {
		if !m.pathWithinSelf(dir) {
			return false
		}
	}
	return true
}

func (m *Mods) pathWithinSelf(path string) bool {
	workspace := m.Config.ResolveWorkspace()
	if strings.TrimSpace(workspace.Canonical) == "" || strings.TrimSpace(path) == "" {
		return false
	}
	roots := []string{
		filepath.Join(workspace.Canonical, selfLayerRel),
		filepath.Join(workspace.Abs, selfLayerRel),
	}
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

// extractPatchPaths returns the workspace-relative file paths referenced by a
// unified diff patch payload (the fs_apply_patch tool input). Each path is
// normalized the same way as validatePatchPaths in internal/tools.
func extractPatchPaths(data []byte) ([]string, error) {
	var parsed struct {
		Patch string `json:"patch"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("patch is malformed: %w", err)
	}
	patch := strings.ReplaceAll(parsed.Patch, "\r\n", "\n")
	if strings.TrimSpace(patch) == "" {
		return nil, fmt.Errorf("patch is missing")
	}
	var paths []string
	seen := make(map[string]bool)
	for _, line := range strings.Split(patch, "\n") {
		raw := line
		switch {
		case strings.HasPrefix(raw, "+++ "):
			raw = raw[4:]
		case strings.HasPrefix(raw, "--- "):
			raw = raw[4:]
		default:
			continue
		}
		path := strings.TrimSpace(raw)
		if path == "/dev/null" {
			continue
		}
		fields := strings.Fields(path)
		if len(fields) == 0 {
			continue
		}
		path = fields[0]
		if strings.HasPrefix(path, "a/") || strings.HasPrefix(path, "b/") {
			path = path[2:]
		}
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("patch references no file paths")
	}
	return paths, nil
}
