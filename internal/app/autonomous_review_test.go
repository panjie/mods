package app

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutonomousWorkspaceToolAuthorization(t *testing.T) {
	workspace := t.TempDir()
	registry := testReviewRegistry(t)

	t.Run("allows workspace file writes", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_write_file", []byte(`{"path":"internal/file.go","content":"x"}`))
		require.NoError(t, err)
	})

	t.Run("rejects outside file writes", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		outside := filepath.Join(t.TempDir(), "file.go")
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_write_file", []byte(`{"path":"`+filepath.ToSlash(outside)+`","content":"x"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the workspace")
	})

	t.Run("allows read-only shell", func(t *testing.T) {
		mods := &Mods{
			ctx:    context.Background(),
			Config: &Config{},
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: false}
			},
		}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"go test ./..."}`))
		require.NoError(t, err)
	})

	t.Run("allows workspace shell writes", func(t *testing.T) {
		mods := &Mods{
			ctx:    context.Background(),
			Config: &Config{},
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{filepath.Join(workspace, "internal")},
				}
			},
		}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"go test ./internal/..."}`))
		require.NoError(t, err)
	})

	t.Run("rejects shell with unknown affected dirs", func(t *testing.T) {
		mods := &Mods{
			ctx:    context.Background(),
			Config: &Config{},
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{NeedsReview: true}
			},
		}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"some unsupported writer"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "affected directories are unknown")
	})

	t.Run("rejects shell outside workspace", func(t *testing.T) {
		mods := &Mods{
			ctx:    context.Background(),
			Config: &Config{},
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{t.TempDir()},
				}
			},
		}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"write outside"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the workspace")
	})
}
