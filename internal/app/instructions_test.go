package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/require"

	"github.com/panjie/mods/internal/proto"
)

func TestLoadProjectInstructions(t *testing.T) {
	cfgWith := func(root string) *Config {
		cfg := &Config{}
		cfg.WorkingDir = root
		return cfg
	}
	writeAgents := func(t *testing.T, root, body string) {
		t.Helper()
		require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(body), 0o644))
	}

	t.Run("nil config is safe", func(t *testing.T) {
		require.Empty(t, loadProjectInstructions(nil))
	})

	t.Run("disabled by flag", func(t *testing.T) {
		root := t.TempDir()
		writeAgents(t, root, "# Agents\nUse task check.")
		cfg := cfgWith(root)
		cfg.NoInstructions = true
		require.Empty(t, loadProjectInstructions(cfg))
	})

	t.Run("disabled in minimal mode", func(t *testing.T) {
		root := t.TempDir()
		writeAgents(t, root, "# Agents\nUse task check.")
		cfg := cfgWith(root)
		cfg.Minimal = true
		require.Empty(t, loadProjectInstructions(cfg))
	})

	t.Run("missing file is empty (not an error)", func(t *testing.T) {
		require.Empty(t, loadProjectInstructions(cfgWith(t.TempDir())))
	})

	t.Run("present file returns content", func(t *testing.T) {
		root := t.TempDir()
		writeAgents(t, root, "# Agents\nBuild: `task check` then `task test`.")
		got := loadProjectInstructions(cfgWith(root))
		require.Contains(t, got, "task check")
		require.Contains(t, got, "task test")
	})

	t.Run("large file is truncated", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Repeat("a", maxInstructionsBytes+500)
		writeAgents(t, root, body)
		got := loadProjectInstructions(cfgWith(root))
		require.Contains(t, got, "truncated")
		require.Less(t, len(got), len(body))
	})

	t.Run("unicode truncation remains valid UTF-8", func(t *testing.T) {
		root := t.TempDir()
		body := strings.Repeat("你", maxInstructionsBytes/3+2)
		writeAgents(t, root, body)
		got := loadProjectInstructions(cfgWith(root))
		require.True(t, utf8.ValidString(got))
		require.Contains(t, got, "truncated")
	})
}

// TestSetupStreamContextInjectsAgentsMD verifies the end-to-end wiring:
// setupStreamContext injects AGENTS.md as a system message, and --no-instructions
// suppresses it.
func TestSetupStreamContextInjectsAgentsMD(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nBuild with `task check`."), 0o644))

	newMods := func(modify func(*Config)) *Mods {
		cfg := Config{}
		cfg.WorkingDir = root
		if modify != nil {
			modify(&cfg)
		}
		cfg.ApplyDefaults()
		cfg.Roles = map[string][]string{}
		return &Mods{Config: &cfg, Styles: makeStyles(true), ctx: context.Background()}
	}
	systemJoin := func(m *Mods) string {
		var sb strings.Builder
		for _, msg := range m.messages {
			if msg.Role == proto.RoleSystem {
				sb.WriteString(msg.Content)
				sb.WriteByte('\n')
			}
		}
		return sb.String()
	}

	t.Run("injected by default", func(t *testing.T) {
		m := newMods(nil)
		require.NoError(t, m.setupStreamContext("hello"))
		joined := systemJoin(m)
		require.Contains(t, joined, "Project instructions (AGENTS.md)")
		require.Contains(t, joined, "task check")
	})

	t.Run("suppressed by no-instructions", func(t *testing.T) {
		m := newMods(func(c *Config) { c.NoInstructions = true })
		require.NoError(t, m.setupStreamContext("hello"))
		require.NotContains(t, systemJoin(m), "Project instructions (AGENTS.md)")
	})

	t.Run("suppressed in minimal mode", func(t *testing.T) {
		m := newMods(func(c *Config) { c.Minimal = true })
		require.NoError(t, m.setupStreamContext("hello"))
		require.NotContains(t, systemJoin(m), "Project instructions (AGENTS.md)")
	})
}

// TestProjectInstructionsNotice covers the one-time tool-style record that
// announces AGENTS.md was loaded. The record follows the same visibility
// switches as tool status records and is emitted once per process.
func TestProjectInstructionsNotice(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nBuild with `task check`."), 0o644))

	newMods := func(modify func(*Config)) *Mods {
		cfg := Config{}
		cfg.WorkingDir = root
		cfg.ApplyDefaults()
		cfg.Roles = map[string][]string{}
		cfg.InteractiveTTYAvailable = true
		cfg.WordWrap = 200
		if modify != nil {
			modify(&cfg)
		}
		return &Mods{Config: &cfg, Styles: makeStyles(true), showOperationStatus: true, width: 200}
	}

	t.Run("announces the loaded file once per process", func(t *testing.T) {
		m := newMods(nil)
		line := m.projectInstructionsNoticeLine()
		require.Contains(t, line, "Project instructions:")
		require.Contains(t, line, filepath.Join(root, "AGENTS.md"))

		require.NotNil(t, m.emitProjectInstructionsNotice())
		require.True(t, m.Config.ProjectInstructionsNoticeShown)
		require.Nil(t, m.emitProjectInstructionsNotice())
	})

	t.Run("hidden by no-instructions", func(t *testing.T) {
		m := newMods(func(c *Config) { c.NoInstructions = true })
		require.Empty(t, m.projectInstructionsNoticeLine())
	})

	t.Run("hidden in minimal mode", func(t *testing.T) {
		m := newMods(func(c *Config) { c.Minimal = true })
		require.Empty(t, m.projectInstructionsNoticeLine())
	})

	t.Run("hidden when tool status is hidden", func(t *testing.T) {
		m := newMods(nil)
		m.showOperationStatus = false
		require.Empty(t, m.projectInstructionsNoticeLine())
	})

	t.Run("hidden without an interactive terminal", func(t *testing.T) {
		m := newMods(func(c *Config) { c.InteractiveTTYAvailable = false })
		require.Empty(t, m.projectInstructionsNoticeLine())
	})

	t.Run("hidden when AGENTS.md is missing", func(t *testing.T) {
		m := newMods(func(c *Config) { c.WorkingDir = t.TempDir() })
		require.Empty(t, m.projectInstructionsNoticeLine())
	})

	t.Run("hidden when AGENTS.md is blank", func(t *testing.T) {
		blankRoot := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(blankRoot, "AGENTS.md"), []byte("\n\n"), 0o644))
		m := newMods(func(c *Config) { c.WorkingDir = blankRoot })
		require.Empty(t, m.projectInstructionsNoticeLine())
	})
}
