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

func TestAnalyzeShellCommandPowerShellNotMatchRegexIsWorkspaceRead(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return defaultShellCommandAnalysis()
		},
	}
	cmd := `Get-ChildItem -Recurse -Include *.go | Where-Object { $_.FullName -notmatch '\\(vendor|\.git|bin)\\' } | Get-Content | Measure-Object -Line | Select-Object -ExpandProperty Lines`

	got := m.analyzeShellCommand("powershell_run", cmd)

	require.False(t, got.NeedsReview)
	require.Equal(t, shellEffectRead, got.Effect)
	require.Equal(t, []string{workspace}, got.AffectedDirs)
}

func TestAnalyzeShellCommandPowerShellProfileWriteKeepsRuntimeTargetsUnresolved(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for known PowerShell writers: %s", command)
			return defaultShellCommandAnalysis()
		},
	}
	cmd := `$prof = $PROFILE.CurrentUserCurrentHost; $dir = Split-Path $prof; if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }; Set-Content -Path $prof -Value "Invoke-Expression (&starship init powershell)" -Encoding UTF8`

	got := m.analyzeShellCommand("powershell_run", cmd)

	require.True(t, got.NeedsReview)
	require.Equal(t, shellEffectWrite, got.Effect)
	require.Empty(t, got.AffectedDirs)
	require.Contains(t, got.UnresolvedPaths, `$dir`)
	require.Contains(t, got.UnresolvedPaths, `$prof`)
	for _, dir := range got.AffectedDirs {
		require.NotContains(t, dir, `$PROFILE`)
		require.NotContains(t, dir, `$prof`)
	}
}
