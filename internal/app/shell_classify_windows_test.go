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

	got := m.assessCommand("shell_run", cmd)
	require.Equal(t, approval.EffectRead, got.Effect)
	require.NotEmpty(t, got.Reason)
}

func TestAnalyzeShellCommandPowerShellUserProfileDoesNotInventPlaceholder(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)

	m := &Mods{
		Config: testConfigForWorkspace(t.TempDir()),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	cmd := `Select-String -Path "$env:USERPROFILE\.config\mods\mods.yml" -Pattern "default-api|default-model|base-url|model" | Select-Object -First 20`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Len(t, got.KnownDirs, 1)
	require.True(t, strings.HasPrefix(strings.ToLower(got.KnownDirs[0]), strings.ToLower(home)), got.KnownDirs)
	require.NotContains(t, got.KnownDirs[0], "<user>")
}

func TestAnalyzeShellCommandPowerShellNotMatchRegexIsWorkspaceRead(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	cmd := `Get-ChildItem -Recurse -Include *.go | Where-Object { $_.FullName -notmatch '\\(vendor|\.git|bin)\\' } | Get-Content | Measure-Object -Line | Select-Object -ExpandProperty Lines`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, []string{workspace}, got.KnownDirs)
}

func TestAnalyzeShellCommandPowerShellProfileWriteKeepsRuntimeTargetsUnresolved(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for known PowerShell writers: %s", command)
			return approval.UnknownCommandAssessment()
		},
	}
	cmd := `$prof = $PROFILE.CurrentUserCurrentHost; $dir = Split-Path $prof; if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }; Set-Content -Path $prof -Value "Invoke-Expression (&starship init powershell)" -Encoding UTF8`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, got.Effect)
	require.Empty(t, got.KnownDirs)
	require.Contains(t, got.DynamicTargets, `$dir`)
	require.Contains(t, got.DynamicTargets, `$prof`)
	for _, dir := range got.KnownDirs {
		require.NotContains(t, dir, `$PROFILE`)
		require.NotContains(t, dir, `$prof`)
	}
}

func TestAnalyzeShellCommandPowerShellDynamicProfileInspectionStaysReadOnly(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, _ string) approval.CommandAssessment {
			return approval.CommandAssessment{
				Effect: approval.EffectRead,
				Reason: "inspects PowerShell profile paths and WinGet links",
			}
		},
	}
	cmd := `Write-Output "=== All profile paths ==="; $PROFILE | Format-List *; Write-Output "=== Which exist? ==="; $PROFILE.PSObject.Properties | ForEach-Object { $v = $_.Value; "{0,-45} exists={1}" -f $v, (Test-Path $v) }; Write-Output "=== WinGet links dir (shim check) ==="; Get-ChildItem "C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links" -ErrorAction SilentlyContinue | Select-Object Name`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, AccessRead, got.AccessIntent().Class)
	require.Contains(t, got.DynamicTargets, `$v`)
}

func TestAnalyzeShellCommandPowerShellProfileProbeIsCompound(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{Config: testConfigForWorkspace(t.TempDir())}
	cmd := `"USERPROFILE=$env:USERPROFILE"; "Profile: $PROFILE"; "ProfileExists: $(Test-Path $PROFILE)"; Get-ChildItem $env:USERPROFILE\Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, approval.ReviewabilityCompound, got.Reviewability.Level)
	require.True(t, got.Reviewability.ShouldCorrect)
	require.Equal(t, 4, got.Shape.TopLevelActions)
	require.Contains(t, got.Reviewability.Reasons, approval.ReviewabilityMultipleIndependent)
	require.Error(t, newCommandPreflightGate(m.Config).check("powershell_run", got))
}

func TestAssessCommandPowerShellStandardDynamicReads(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkspace(t.TempDir()),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	tests := []struct {
		command string
		target  string
	}{
		{`$PROFILE.CurrentUserCurrentHost; Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost`, `$PROFILE.CurrentUserCurrentHost`},
		{`$env:STARSHIP_CONFIG; Test-Path $env:STARSHIP_CONFIG`, `$env:STARSHIP_CONFIG`},
	}
	for _, tc := range tests {
		assessment := m.assessCommand("powershell_run", tc.command)
		require.Equal(t, approval.EffectRead, assessment.Effect)
		require.Contains(t, assessment.DynamicTargets, tc.target)
		require.Equal(t, AccessRead, assessment.AccessIntent().Class)
		require.Equal(t, []string{tc.target}, assessment.AccessIntent().UnresolvedPaths)
		require.Equal(t, DecisionAllow, ClassifyAccess(assessment.AccessIntent(), WorkspaceScope(m.Config.ResolveWorkspace().Canonical), nil, ApprovalReviewMode(ReviewAuto)))
		require.Equal(t, approval.ReviewabilityCompound, assessment.Reviewability.Level)
		require.True(t, assessment.Reviewability.ShouldCorrect)
	}
}

func TestAssessCommandPowerShellProfileObjectProbeAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	command := `[PSCustomObject]@{ Path = $PROFILE.CurrentUserCurrentHost; Exists = Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost } | ConvertTo-Json -Compress`
	assessment := m.assessCommand("powershell_run", command)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Contains(t, assessment.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	require.Equal(t, approval.ReviewabilitySimple, assessment.Reviewability.Level)
	require.Equal(t, []string{`$PROFILE.CurrentUserCurrentHost`}, assessment.AccessIntent().UnresolvedPaths)
	require.Equal(t, DecisionAllow, ClassifyAccess(assessment.AccessIntent(), WorkspaceScope(workspace), nil, ApprovalReviewMode(ReviewAuto)))
}
