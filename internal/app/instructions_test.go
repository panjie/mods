package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/stretchr/testify/require"

	"github.com/panjie/mods/internal/proto"
)

func TestLoadProjectInstructions(t *testing.T) {
	cfgWith := func(root string) *Config {
		cfg := &Config{}
		cfg.BuiltinTools.Workspace = root
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
}

// TestSetupStreamContextInjectsAgentsMD verifies the end-to-end wiring:
// setupStreamContext injects AGENTS.md as a system message, and --no-instructions
// suppresses it.
func TestSetupStreamContextInjectsAgentsMD(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("# Agents\nBuild with `task check`."), 0o644))

	newMods := func(modify func(*Config)) *Mods {
		cfg := Config{}
		cfg.BuiltinTools.Workspace = root
		if modify != nil {
			modify(&cfg)
		}
		cfg.ApplyDefaults()
		cfg.Roles = map[string][]string{}
		return &Mods{Config: &cfg, Styles: makeStyles(lipgloss.NewRenderer(nil)), ctx: context.Background()}
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
		require.NoError(t, m.setupStreamContext("hello", Model{MaxChars: 1000}))
		joined := systemJoin(m)
		require.Contains(t, joined, "Project instructions (AGENTS.md)")
		require.Contains(t, joined, "task check")
	})

	t.Run("suppressed by no-instructions", func(t *testing.T) {
		m := newMods(func(c *Config) { c.NoInstructions = true })
		require.NoError(t, m.setupStreamContext("hello", Model{MaxChars: 1000}))
		require.NotContains(t, systemJoin(m), "Project instructions (AGENTS.md)")
	})

	t.Run("suppressed in minimal mode", func(t *testing.T) {
		m := newMods(func(c *Config) { c.Minimal = true })
		require.NoError(t, m.setupStreamContext("hello", Model{MaxChars: 1000}))
		require.NotContains(t, systemJoin(m), "Project instructions (AGENTS.md)")
	})
}
