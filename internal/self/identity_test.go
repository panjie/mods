package self

import (
	"strings"
	"testing"

	cfgpkg "github.com/panjie/mods/internal/config"
	"github.com/stretchr/testify/require"
)

func TestIdentityContextIncludesBoundaries(t *testing.T) {
	cfg := cfgpkg.Default()
	cfg.BuiltinTools.Filesystem = "auto"
	cfg.BuiltinTools.Shell = true
	cfg.BuiltinTools.SequentialThinking = true
	cfg.ReviewMode = cfgpkg.ReviewMutable

	content := IdentityContext(&cfg, cfgpkg.Workspace{
		Display: "/workspace",
	})

	require.Contains(t, content, "Mods is a terminal AI agent")
	require.Contains(t, content, "current workspace root is /workspace")
	require.Contains(t, content, "filesystem=auto")
	require.Contains(t, content, "shell=true")
	require.Contains(t, content, "review mode is mutable")
	require.Contains(t, content, "Evolution evaluations are local workspace-scoped state")
	require.Contains(t, content, "With explicit --evolve-auto")
	require.Contains(t, content, "without proposal approval")
	require.Contains(t, content, "refusing workspace-external file writes")
	require.False(t, strings.Contains(content, "already can autonomously modify"))
}
