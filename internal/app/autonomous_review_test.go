package app

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAutonomousWorkspaceToolAuthorization(t *testing.T) {
	workspace := t.TempDir()
	registry := testReviewRegistry(t)
	selfDir := filepath.Join(workspace, "internal", "self")

	t.Run("allows self layer file writes", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_write_file", []byte(`{"path":"internal/self/identity.go","content":"x"}`))
		require.NoError(t, err)
	})

	t.Run("rejects workspace file writes outside the self layer", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_write_file", []byte(`{"path":"internal/app/file.go","content":"x"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the self layer")
	})

	t.Run("rejects file writes outside the workspace", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		outside := filepath.Join(t.TempDir(), "file.go")
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_write_file", []byte(`{"path":"`+filepath.ToSlash(outside)+`","content":"x"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the self layer")
	})

	t.Run("allows apply patch within the self layer", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		patch := "--- a/internal/self/identity.go\n+++ b/internal/self/identity.go\n@@\n-x\n+y\n"
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
		require.NoError(t, err)
	})

	t.Run("rejects apply patch outside the self layer", func(t *testing.T) {
		mods := &Mods{ctx: context.Background(), Config: &Config{}}
		mods.Config.BuiltinTools.Workspace = workspace
		patch := "--- a/internal/app/file.go\n+++ b/internal/app/file.go\n@@\n-x\n+y\n"
		err := mods.authorizeAutonomousWorkspaceTool(registry, "fs_apply_patch", []byte(`{"patch":`+strconv.Quote(patch)+`}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the self layer")
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

	t.Run("allows self layer shell writes", func(t *testing.T) {
		mods := &Mods{
			ctx:    context.Background(),
			Config: &Config{},
			shellAnalyzer: func(string, string) shellCommandAnalysis {
				return shellCommandAnalysis{
					NeedsReview:  true,
					AffectedDirs: []string{selfDir},
				}
			},
		}
		mods.Config.BuiltinTools.Workspace = workspace
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"go test ./internal/self/..."}`))
		require.NoError(t, err)
	})

	t.Run("rejects shell writes outside the self layer", func(t *testing.T) {
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
		err := mods.authorizeAutonomousWorkspaceTool(registry, "shell_run", []byte(`{"command":"touch internal/app/x"}`))
		require.Error(t, err)
		require.Contains(t, err.Error(), "outside the self layer")
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

	t.Run("rejects shell outside the workspace", func(t *testing.T) {
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
		require.Contains(t, err.Error(), "outside the self layer")
	})
}
