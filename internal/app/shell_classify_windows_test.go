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
	// The engine-automatic $PROFILE.CurrentUserCurrentHost reference resolves
	// to a concrete path; the command-local $prof / $dir stay runtime-resolved.
	require.Len(t, got.KnownDirs, 1)
	require.Contains(t, strings.ToLower(got.KnownDirs[0]), "profile", got.KnownDirs)
	require.Contains(t, got.DynamicTargets, `$dir`)
	require.Contains(t, got.DynamicTargets, `$prof`)
	require.NotContains(t, got.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	for _, dir := range got.KnownDirs {
		require.NotContains(t, dir, `$PROFILE`)
		require.NotContains(t, dir, `$prof`)
	}
}

func TestAssessCommandPowerShellProfileWriteResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for known PowerShell writers: %s", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Set-Content -Path $PROFILE -Value "Import-Module foo"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "an engine-automatic write target resolves to a concrete path")
	require.Len(t, assessment.KnownDirs, 1)
	require.Contains(t, strings.ToLower(assessment.KnownDirs[0]), "profile", assessment.KnownDirs)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "an external write still asks once, but the concrete dir makes the approval rule-saveable")
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
		require.True(t, assessment.AccessIntent().DynamicProbe)
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
	require.True(t, assessment.AccessIntent().DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(assessment.AccessIntent(), WorkspaceScope(workspace), nil, ApprovalReviewMode(ReviewAuto)))
}

func TestAssessCommandPowerShellDynamicContentReadRequiresReview(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Get-Content -LiteralPath $env:AWS_SHARED_CREDENTIALS_FILE`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:AWS_SHARED_CREDENTIALS_FILE`)
	require.False(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellOutputProbeWithEnvRequiresReview(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	tests := []struct {
		name    string
		command string
	}{
		{"bare env output", `Write-Output $env:GITHUB_TOKEN`},
		{"formatted env output", `ConvertTo-Json $env:STARSHIP_CONFIG`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := m.assessCommand("powershell_run", tc.command)

			require.Equal(t, approval.EffectRead, assessment.Effect)
			require.False(t, assessment.DynamicProbe)
			require.Equal(t, DecisionAsk, ClassifyAccess(
				assessment.AccessIntent(),
				WorkspaceScope(workspace),
				nil,
				ApprovalReviewMode(ReviewAuto),
			))
		})
	}
}

func TestAssessCommandPowerShellPathProbeWithEnvAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Test-Path $env:STARSHIP_CONFIG`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:STARSHIP_CONFIG`)
	require.True(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellOutputProbeWithProfileAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Write-Output $PROFILE.CurrentUserCurrentHost`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	require.True(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellStableEnvPathReadResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Get-ChildItem -Path "$env:SystemRoot\WinSxS" -Filter "*.dll" | Select-Object -First 3`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Len(t, assessment.KnownDirs, 1)
	require.True(t, strings.EqualFold(assessment.KnownDirs[0], systemRoot+`\WinSxS`), assessment.KnownDirs)
	require.Empty(t, assessment.DynamicTargets)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	), "an external read still asks once, but the concrete dir makes the approval rule-saveable")
}

func TestAssessCommandPowerShellStableEnvAssignmentKeepsDynamicTargets(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, _ string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "reads notes"}
		},
	}
	assessment := m.assessCommand("powershell_run", `$env:TEMP = "C:\Users\Test\Documents"; Get-Content "$env:TEMP\notes.txt"`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:TEMP\notes.txt`, "an env reassignment must keep the target runtime-resolved")
	require.Equal(t, []string{`C:\Users\Test\Documents`}, assessment.KnownDirs,
		"the assigned literal is still extracted; the process TEMP location must not be substituted")
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellStableEnvWriteResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for known PowerShell writers: %s", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Set-Content -Path "$env:TEMP\profile_init.el" -Value "x"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a stable-env write target resolves to a concrete directory")
	require.Len(t, assessment.KnownDirs, 1)
	require.True(t, strings.HasPrefix(strings.ToLower(assessment.KnownDirs[0]), strings.ToLower(os.TempDir())), assessment.KnownDirs)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "a write into the safe temp directory matches the allow cell of the approval matrix")
}

func TestAssessCommandPowerShellStableEnvWriteAssignmentKeepsDynamicTargets(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkspace(t.TempDir()),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for known PowerShell writers: %s", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `$env:TEMP = "C:\Users\Test\Documents"; Set-Content -Path "$env:TEMP\foo.el" -Value "x"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:TEMP\foo.el`, "an env reassignment must keep the write target runtime-resolved")
}

func TestAssessCommandPowerShellStableEnvProbeStaysDynamic(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	workspace := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkspace(workspace),
		shellAnalyzer: func(_, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	assessment := m.assessCommand("powershell_run", `Resolve-Path $env:SystemRoot\WinSxS`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.True(t, assessment.DynamicProbe)
	require.NotEmpty(t, assessment.DynamicTargets)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkspaceScope(workspace),
		nil,
		ApprovalReviewMode(ReviewAuto),
	), "path-resolution probes keep their dynamic-target auto-allow semantics")
}

func TestResolveStableEnvTargets(t *testing.T) {
	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")

	known, dynamic := resolveStableEnvTargets(nil, []string{`$env:SystemRoot`, `$env:SystemRoot\WinSxS`}, `C:\ws`)
	require.Len(t, known, 1)
	require.True(t, strings.EqualFold(known[0], systemRoot+`\WinSxS`), known)
	require.Empty(t, dynamic, "the bare variable reference is subsumed by its expanded path form")

	known, dynamic = resolveStableEnvTargets(nil, []string{`$env:STARSHIP_CONFIG`, `$env:SystemRoot\WinSxS`}, `C:\ws`)
	require.Len(t, known, 1)
	require.Equal(t, []string{`$env:STARSHIP_CONFIG`}, dynamic, "non-stable targets stay dynamic")

	known, dynamic = resolveStableEnvTargets([]string{known[0]}, []string{`$env:SystemRoot\WinSxS`}, `C:\ws`)
	require.Len(t, known, 1, "already-known concrete dirs are not duplicated")
	require.Empty(t, dynamic)

	known, dynamic = resolveStableEnvTargets(nil, []string{`$env:SECRET\x`}, `C:\ws`)
	require.Empty(t, known)
	require.Equal(t, []string{`$env:SECRET\x`}, dynamic)

	known, dynamic = resolveStableEnvTargets([]string{`C:\ws`}, nil, `C:\ws`)
	require.Equal(t, []string{`C:\ws`}, known)
	require.Nil(t, dynamic)
}

func TestAssessCommandPowerShellMisparsedLiteralNotDynamic(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkspace(t.TempDir()),
		shellAnalyzer: func(_, _ string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "prints emacs init time"}
		},
	}
	cmd := `emacs --batch --eval "(progn (message \"load-time: %s\" (emacs-init-time)) (kill-emacs))" 2>&1`

	assessment := m.assessCommand("shell_run", cmd)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "mis-parsed POSIX --eval fragments must not become dynamic targets")
}

func TestCommandMutatesPowerShellEnvironment(t *testing.T) {
	mutations := []string{
		`$env:TEMP = "C:\elsewhere"`,
		`$env:TEMP += "suffix"`,
		`${env:TEMP} = "C:\elsewhere"`,
		`Set-Item -Path Env:TEMP -Value "C:\elsewhere"`,
		`Remove-Item Env:\TEMP`,
		`New-Item -Path Env:TRACKER -Value 1`,
		`Set-Content -Path Env:\TEMP -Value 'x'`,
		`[Environment]::SetEnvironmentVariable('TEMP', 'C:\elsewhere', 'Process')`,
	}
	for _, command := range mutations {
		require.True(t, commandMutatesPowerShellEnvironment(command), command)
	}
	reads := []string{
		`Get-Content $env:TEMP\notes.txt`,
		`Test-Path $env:SystemRoot\WinSxS`,
		`Write-Output ($env:TEMP -eq 'x')`,
		`Set-Content -Path "$env:TEMP\profile_init.el" -Value "x"`,
	}
	for _, command := range reads {
		require.False(t, commandMutatesPowerShellEnvironment(command), command)
	}
	require.True(t, commandMutatesPowerShellEnvironment(`Write-Output "$env:USERNAME=$env:TEMP"`),
		"an equals sign right after an env name is treated as a mutation (conservative)")
}
