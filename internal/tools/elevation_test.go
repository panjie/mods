package tools

import (
	"context"
	"os/exec"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestElevationSchemaAndGuidance(t *testing.T) {
	registry := NewRegistry()
	root := t.TempDir()
	require.NoError(t, RegisterProcess(registry, ProcessConfig{Root: root}))
	require.NoError(t, RegisterShell(registry, ShellConfig{Root: root}))
	require.NoError(t, RegisterPowerShell(registry, ShellConfig{Root: root}))
	for _, name := range []string{"process_run", "shell_run", "powershell_run"} {
		tool, ok := registry.Tool(name)
		require.True(t, ok)
		properties := tool.Spec.InputSchema["properties"].(map[string]any)
		if runtime.GOOS == "windows" {
			require.Contains(t, properties, "elevated")
			require.Contains(t, tool.Spec.Description, "after ordinary approval")
			require.Contains(t, tool.Spec.Description, "elevated=true")
		} else {
			require.NotContains(t, properties, "elevated")
			require.NotContains(t, tool.Spec.Description, "elevated=true")
		}
	}
}

func TestElevationUnsupportedPlatform(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX behavior")
	}
	_, err := (commandRunner{Elevated: true, BuildCommand: func(context.Context) *exec.Cmd {
		t.Fatal("unsupported elevation must fail before preparing a command")
		return nil
	}}).Run()
	require.ErrorContains(t, err, "only on Windows")
}
