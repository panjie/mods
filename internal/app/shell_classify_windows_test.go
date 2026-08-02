//go:build windows

package app

import (
	"os"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{Config: testConfigForWorkspace(workspace)}
	cmd := `Get-ChildItem -Recurse -Filter *.go | Select-Object FullName | ForEach-Object { $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines; "$($_.FullName): $lines lines" } | Sort-Object { [int]($_.Split(':')[1].Trim().Split(' ')[0]) } -Descending`

	got := m.analyzeShellCommand("shell_run", cmd)
	require.False(t, got.NeedsReview)
	require.Equal(t, shellEffectRead, got.Effect)
	require.NotEmpty(t, got.Reason)
}

func TestAnalyzeShellCommandPowerShellUserProfileDoesNotInventPlaceholder(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)

	m := &Mods{
		Config: testConfigForWorkspace(t.TempDir()),
		shellAnalyzer: func(_, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return defaultShellCommandAnalysis()
		},
	}
	cmd := `Select-String -Path "$env:USERPROFILE\.config\mods\mods.yml" -Pattern "default-api|default-model|base-url|model" | Select-Object -First 20`

	got := m.analyzeShellCommand("powershell_run", cmd)

	require.False(t, got.NeedsReview)
	require.Equal(t, shellEffectRead, got.Effect)
	require.Len(t, got.AffectedDirs, 1)
	require.True(t, strings.HasPrefix(strings.ToLower(got.AffectedDirs[0]), strings.ToLower(home)), got.AffectedDirs)
	require.NotContains(t, got.AffectedDirs[0], "<user>")
}
