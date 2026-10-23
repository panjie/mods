//go:build windows

package app

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeShellCommandPowerShellLineCountPipelineIsReadOnly(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
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
		Config: testConfigForWorkingDir(t.TempDir()),
	}
	cmd := `Select-String -Path "$env:USERPROFILE\.config\mods\mods.yml" -Pattern "default-api|default-model|base-url|model" | Select-Object -First 20`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Len(t, got.KnownDirs, 1)
	require.True(t, strings.HasPrefix(strings.ToLower(got.KnownDirs[0]), strings.ToLower(home)), got.KnownDirs)
	require.NotContains(t, got.KnownDirs[0], "<user>")
}

func TestAnalyzeShellCommandPowerShellNotMatchRegexIsWorkingDirRead(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `Get-ChildItem -Recurse -Include *.go | Where-Object { $_.FullName -notmatch '\\(vendor|\.git|bin)\\' } | Get-Content | Measure-Object -Line | Select-Object -ExpandProperty Lines`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, []string{cwd}, got.KnownDirs)
}

func TestAnalyzeShellCommandPowerShellProfileWriteKeepsRuntimeTargetsUnresolved(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
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

func TestAssessCommandPowerShellLiteralVariableReadResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `$p="C:\Users\Test\AppData\Roaming\.emacs.d\init.el"; $c=Get-Content $p -Raw; "=== ensure line present: $($c.Contains('use-package-always-ensure t')) ==="; $c | Select-String -Pattern 'always-ensure' -AllMatches`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a literal-assigned variable resolves to a concrete target")
	require.Contains(t, assessment.KnownDirs, `C:\Users\Test\AppData\Roaming\.emacs.d\init.el`)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
}

func TestAssessCommandPowerShellLiteralVariableWriteResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := "$p=\"C:\\Users\\Test\\AppData\\Roaming\\.emacs.d\\init.el\"\n$c=Get-Content $p -Raw\n$new_block=@'\n(require 'package)\n(setq package-archives '((\"gnu\" . \"https://mirrors.tuna.tsinghua.edu.cn/elpa/gnu/\")))\n(package-initialize)\n'@\n$c=$c.Replace(\"old\", $new_block)\nSet-Content -Path $p -Value $c -Encoding UTF8\n\"written\""

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a literal-assigned write target resolves to a concrete directory")
	require.Contains(t, assessment.KnownDirs, `C:\Users\Test\AppData\Roaming\.emacs.d\init.el`)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "an external write still asks once, but the concrete dir makes the approval rule-saveable")
}

func TestAssessCommandPowerShellExpressionAssignmentKeepsDynamicTarget(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `$suffix='\..\outside'; $p='` + cwd + `\init.el'+$suffix; Set-Content -Path $p -Value x`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$p`,
		"a string expression must not be reduced to its literal prefix")
	require.NotContains(t, assessment.LiteralAssignments, "p")
}

func TestAssessCommandPowerShellLiteralVariableWorkingDirReadAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `$p = "` + cwd + `\init.el"; Get-Content $p | Select-Object -Skip 83 -First 5`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, assessment.Effect,
		"a pure-literal top-level assignment must not block the static read-only proof")
	require.Empty(t, assessment.DynamicTargets)
	require.Contains(t, assessment.KnownDirs, cwd+`\init.el`)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "a cwd read via a literal-assigned variable auto-allows")
}

func TestAssessCommandPowerShellLiteralVariableExternalReadAsksWithRule(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	external := `C:\Users\Test\AppData\Roaming\.emacs.d\init.el`
	cmd := `$p = "` + external + `"; Get-Content $p | Select-Object -Skip 83 -First 5`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets)
	require.Contains(t, assessment.KnownDirs, external)
	intent := assessment.AccessIntent()
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)))
}

func TestAssessCommandPowerShellProfileWriteResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Set-Content -Path $PROFILE -Value "Import-Module foo"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "an engine-automatic write target resolves to a concrete path")
	require.Len(t, assessment.KnownDirs, 1)
	require.Contains(t, strings.ToLower(assessment.KnownDirs[0]), "profile", assessment.KnownDirs)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	scope := WorkingDirScope(cwd)
	// $PROFILE lives outside the cwd, so the write still asks once; the
	// concrete resolved directory is what makes the answer saveable as a rule.
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		scope,
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	))
	require.Empty(t, candidateRulesForIntent(
		assessment.AccessIntent(),
		scope,
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "a materialized parent alone does not bind the runtime leaf")
}

func TestAnalyzeShellCommandPowerShellDynamicProfileInspectionStaysReadOnly(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `Write-Output "=== All profile paths ==="; $PROFILE | Format-List *; Write-Output "=== Which exist? ==="; $PROFILE.PSObject.Properties | ForEach-Object { $v = $_.Value; "{0,-45} exists={1}" -f $v, (Test-Path $v) }; Write-Output "=== WinGet links dir (shim check) ==="; Get-ChildItem "C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links" -ErrorAction SilentlyContinue | Select-Object Name`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectUnknown, got.Effect)
	require.Equal(t, AccessWrite, got.AccessIntent().Class)
	require.Contains(t, got.DynamicTargets, `$v`)
}

func TestAnalyzeShellCommandPowerShellProfileProbeIsCompound(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{Config: testConfigForWorkingDir(t.TempDir())}
	cmd := `"USERPROFILE=$env:USERPROFILE"; "Profile: $PROFILE"; "ProfileExists: $(Test-Path $PROFILE)"; Get-ChildItem $env:USERPROFILE\Documents\WindowsPowerShell\Microsoft.PowerShell_profile.ps1 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty FullName`

	got := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, got.Effect)
	require.Equal(t, approval.ReviewabilityCompound, got.Reviewability.Level)
	require.Equal(t, 4, got.Shape.TopLevelActions)
	require.Contains(t, got.Reviewability.Reasons, approval.ReviewabilityMultipleIndependent)
	require.False(t, got.HasReviewabilityConcerns(), "a proven static read needs no complexity diagnostic")
}

func TestAssessCommandPowerShellStandardDynamicReads(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
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
		require.Equal(t, DecisionAllow, ClassifyAccess(assessment.AccessIntent(), WorkingDirScope(m.Config.ResolveWorkingDir().Canonical), nil, ApprovalReviewMode(ReviewAuto)))
		require.Equal(t, approval.ReviewabilityCompound, assessment.Reviewability.Level)
	}
}

func TestAssessCommandPowerShellProfileObjectProbeAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	command := `[PSCustomObject]@{ Path = $PROFILE.CurrentUserCurrentHost; Exists = Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost } | ConvertTo-Json -Compress`
	assessment := m.assessCommand("powershell_run", command)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Contains(t, assessment.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	require.Equal(t, approval.ReviewabilitySimple, assessment.Reviewability.Level)
	require.Equal(t, []string{`$PROFILE.CurrentUserCurrentHost`}, assessment.AccessIntent().UnresolvedPaths)
	require.True(t, assessment.AccessIntent().DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(assessment.AccessIntent(), WorkingDirScope(cwd), nil, ApprovalReviewMode(ReviewAuto)))
}

func TestAssessCommandPowerShellDynamicContentReadRequiresReview(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Get-Content -LiteralPath $env:AWS_SHARED_CREDENTIALS_FILE`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:AWS_SHARED_CREDENTIALS_FILE`)
	require.False(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellOutputProbeWithEnvRequiresReview(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
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
			require.Equal(t, DecisionAllow, ClassifyAccess(
				assessment.AccessIntent(),
				WorkingDirScope(cwd),
				nil,
				ApprovalReviewMode(ReviewAuto),
			))
		})
	}
}

func TestAssessCommandPowerShellPathProbeWithEnvAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Test-Path $env:STARSHIP_CONFIG`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:STARSHIP_CONFIG`)
	require.True(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellOutputProbeWithProfileAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Write-Output $PROFILE.CurrentUserCurrentHost`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$PROFILE.CurrentUserCurrentHost`)
	require.True(t, assessment.DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellStableEnvPathReadResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	systemRoot := os.Getenv("SystemRoot")
	require.NotEmpty(t, systemRoot, "SystemRoot must be set on Windows")

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Get-ChildItem -Path "$env:SystemRoot\WinSxS" -Filter "*.dll" | Select-Object -First 3`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Len(t, assessment.KnownDirs, 1)
	require.True(t, strings.EqualFold(assessment.KnownDirs[0], systemRoot+`\WinSxS`), assessment.KnownDirs)
	require.Empty(t, assessment.DynamicTargets)
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellStableEnvAssignmentKeepsDynamicTargets(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `$env:TEMP = "C:\Users\Test\Documents"; Get-Content "$env:TEMP\notes.txt"`)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:TEMP\notes.txt`, "an env reassignment must keep the target runtime-resolved")
	require.Equal(t, []string{`C:\Users\Test\Documents`}, assessment.KnownDirs,
		"the assigned literal is still extracted; the process TEMP location must not be substituted")
	require.Equal(t, DecisionAsk, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellStableEnvWriteResolvesConcreteDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Set-Content -Path "$env:TEMP\profile_init.el" -Value "x"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a stable-env write target resolves to a concrete directory")
	require.Len(t, assessment.KnownDirs, 1)
	require.True(t, strings.HasPrefix(strings.ToLower(assessment.KnownDirs[0]), strings.ToLower(os.TempDir())), assessment.KnownDirs)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		approval.SafeDirs(),
		ApprovalReviewMode(ReviewAuto),
	), "a write into the safe temp directory matches the allow cell of the approval matrix")
}

func TestAssessCommandPowerShellStableEnvWriteAssignmentKeepsDynamicTargets(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
	}
	assessment := m.assessCommand("powershell_run", `$env:TEMP = "C:\Users\Test\Documents"; Set-Content -Path "$env:TEMP\foo.el" -Value "x"`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:TEMP\foo.el`, "an env reassignment must keep the write target runtime-resolved")
}

func TestAssessCommandPowerShellSystemEnvironmentMutationKeepsDynamicTargets(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
	}
	command := `[System.Environment]::SetEnvironmentVariable('TEMP', [IO.Path]::Combine($HOME, 'elsewhere'), 'Process'); Set-Content -Path "$env:TEMP\foo.el" -Value "x"`
	assessment := m.assessCommand("powershell_run", command)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:TEMP\foo.el`,
		"a fully-qualified environment mutation must suppress expansion of the process TEMP value")
}

func TestAssessCommandPowerShellStableEnvProbeStaysDynamic(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	assessment := m.assessCommand("powershell_run", `Resolve-Path $env:SystemRoot\WinSxS`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.True(t, assessment.DynamicProbe)
	require.NotEmpty(t, assessment.DynamicTargets)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	), "path-resolution probes keep their dynamic-target auto-allow semantics")
}

func TestAssessCommandPowerShellArrayEnvTargetsResolve(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	temp := os.Getenv("TEMP")
	require.NotEmpty(t, temp, "TEMP must be set on Windows")
	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	command := `Remove-Item "$env:TEMP\tts_test.wav","$env:TEMP\tts_en.wav","$env:TEMP\t_ja.wav","$env:TEMP\t_en.wav","$env:TEMP\t_auto2.wav" -ErrorAction SilentlyContinue; "cleaned"`

	assessment := m.assessCommand("powershell_run", command)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "each quoted array element expands to a concrete path instead of one flattened dynamic token")
	intent := assessment.AccessIntent()
	require.False(t, intent.HasUnresolvedPaths())
	dirs := intent.AllDirs()
	require.NotEmpty(t, dirs)
	for _, dir := range dirs {
		lower := strings.ToLower(dir)
		require.True(t, lower == strings.ToLower(temp) || strings.HasPrefix(lower, strings.ToLower(temp)+`\`),
			"every affected path stays inside TEMP: %v", dirs)
	}
	require.Equal(t, "local mutation", shellRiskLevel(assessment, WorkingDirScope(cwd)),
		"the write is no longer classified as a dynamic mutation")
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)),
		"flattened array operands need complete leaf binding before temp exemption")
}

func TestAssessCommandPowerShellQuotedTempPathInMethodCallResolves(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	temp := os.Getenv("TEMP")
	require.NotEmpty(t, temp, "TEMP must be set on Windows")
	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	// Regression: the quoted env path inside ReadAllBytes("...") ends with
	// `")`, which the use scanner used to treat as unbounded concatenation,
	// keeping a bare $env:TEMP dynamic target and triggering a DANGER
	// "Modify a dynamic target" review for a pure temp-dir write.
	command := `git show HEAD:yazi/config/yazi.toml | Out-File -LiteralPath "$env:TEMP\yazi_head.toml" -Encoding utf8NoBOM; $a=[System.IO.File]::ReadAllBytes("$env:TEMP\yazi_head.toml"); $b=[System.IO.File]::ReadAllBytes("yazi.toml"); "headSize=$($a.Length) workSize=$($b.Length)"; for($i=0;$i -lt [Math]::Min($a.Length,$b.Length);$i++){ if($a[$i] -ne $b[$i]){ "firstDiff=$i headByte=$($a[$i]) workByte=$($b[$i])"; break } }`

	assessment := m.assessCommand("powershell_run", command)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a quoted env path inside a .NET method call argument is bounded by its expanded path")
	intent := assessment.AccessIntent()
	require.False(t, intent.HasUnresolvedPaths())
	dirs := intent.AllDirs()
	require.NotEmpty(t, dirs)
	for _, dir := range dirs {
		lower := strings.ToLower(dir)
		require.True(t, lower == strings.ToLower(temp) || strings.HasPrefix(lower, strings.ToLower(temp)+`\`),
			"every affected path stays inside TEMP: %v", dirs)
	}
	require.Equal(t, "local mutation", shellRiskLevel(assessment, WorkingDirScope(cwd)))
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)),
		"method side effects remain incomplete even with a materialized temp path")
}

func TestAssessCommandPowerShellUserProfileReadOffersRuleSaveableDir(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)
	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `Get-Content "$env:USERPROFILE\.emacs.d\init.el" -TotalCount 300 -ErrorAction SilentlyContinue`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "the bare home-variable reference is subsumed by its expanded path form")
	require.False(t, assessment.AccessIntent().HasUnresolvedPaths())
	require.NotEmpty(t, assessment.KnownDirs)
	require.True(t, strings.HasPrefix(strings.ToLower(assessment.KnownDirs[0]), strings.ToLower(home)+`\`), assessment.KnownDirs)
	intent := assessment.AccessIntent()
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(cwd), approval.SafeDirs(), ApprovalReviewMode(ReviewAuto)))
}

func TestAssessCommandPowerShellMisparsedLiteralNotDynamic(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
	}
	cmd := `emacs --batch --eval "(progn (message \"load-time: %s\" (emacs-init-time)) (kill-emacs))" 2>&1`

	assessment := m.assessCommand("shell_run", cmd)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "mis-parsed POSIX --eval fragments must not become dynamic targets")
}

func TestUserProfileEnvReadNeedsNoReviewOrRule(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	home, err := os.UserHomeDir()
	require.NoError(t, err)
	require.NotEmpty(t, home)
	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `Get-Content "$env:USERPROFILE\.emacs.d\init.el" -TotalCount 300 -ErrorAction SilentlyContinue`
	args := []byte(`{"command":` + strconv.Quote(cmd) + `}`)

	assessment := m.assessCommand("powershell_run", cmd)
	intent := assessment.AccessIntent()
	scope := WorkingDirScope(cwd)
	rules := candidateRulesForIntent(intent, scope, approval.SafeDirs(), ApprovalReviewMode(ReviewAuto))
	require.Empty(t, rules)
	reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: scope}
	require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), shellExecution: true, assessment: &assessment, accessIntent: intent}, "powershell_run", args))
}

func TestAssessCommandPowerShellPublicEnvContentReadAutoAllows(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}

	assessment := m.assessCommand("powershell_run", `$env:Path -split ';' | Where-Object { $_ }`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.DynamicTargets, "a public machine-metadata variable carries no capability to review")
	// With no dynamic target left, the read falls back to the cwd
	// scope, which the matrix allows without review.
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellSecretEnvContentReadStillAsks(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}

	assessment := m.assessCommand("powershell_run", `Write-Output $env:GITHUB_TOKEN`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Contains(t, assessment.DynamicTargets, `$env:GITHUB_TOKEN`,
		"a secret-bearing variable is not public metadata and stays dynamic")
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

// TestAssessProcessInvocationPowerShellFlagsAreNotPathFacts pins the reported
// Windows bug: reg.exe switches (/v, /d, /f) share leading-slash syntax with
// POSIX absolute paths and used to become "write dirs" in the review panel and
// its "Always allow" candidate rule. Only genuine literal paths survive.
func TestAssessProcessInvocationPowerShellFlagsAreNotPathFacts(t *testing.T) {
	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
	}
	invoke := `{"program":"reg.exe","args":["add","HKCU\\Software\\Classes\\*\\shell\\EditWithEmacs","/v","Icon","/d","C:\\tools\\emacs\\bin\\emacs.exe,0","/f"]}`

	assessment := m.assessCommand("process_run", invoke)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	for _, flag := range []string{"/v", "/d", "/f"} {
		require.NotContains(t, assessment.KnownDirs, flag,
			"a native-program switch must not become a review target")
	}
	require.Contains(t, assessment.KnownDirs, `C:\tools\emacs\bin\emacs.exe,0`,
		"a genuine literal path argument stays a path fact")
}

func TestAssessCommandPowerShellRegistryWriteStaysOutOfFilesystemRules(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `New-Item -Path "HKCU:\Software\Classes\Neovide" -Force | Out-Null; Set-ItemProperty -Path "HKCU:\Software\Classes\Neovide" -Name "(Default)" -Value "Neovide Text Editor"; Get-ItemProperty "HKCU:\Software\Classes\Neovide" | Select-Object -ExpandProperty "(Default)"`

	assessment := m.assessCommand("powershell_run", cmd)
	intent := normalizeAccessIntentDirs(assessment.AccessIntent(), cwd, "powershell_run", true)
	presentation := formatReviewPresentationWithIntent("powershell_run", []byte(`{"command":`+strconv.Quote(cmd)+`}`), assessment, WorkingDirScope(cwd), intent)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Empty(t, assessment.DynamicTargets)
	require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, assessment.ProviderWriteTargets)
	require.NotContains(t, strings.Join(intent.Dirs, "\n"), `HKCU:`)
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(cwd), nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(cwd), nil, ApprovalReviewMode(ReviewAuto)))
	require.Equal(t, "Modify a PowerShell provider target", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Target", Value: `HKCU:\Software\Classes\Neovide`})
}

func TestAssessCommandPowerShellRegistryAliasWriteStaysOutOfFilesystemRules(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	assessment := m.assessCommand("powershell_run", `sp -PSPath "HKCU:\Software\Classes\Neovide" -Name x -Value y`)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Equal(t, []string{`HKCU:\Software\Classes\Neovide`}, assessment.ProviderWriteTargets)
	require.Empty(t, candidateRulesForIntent(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellDriveMutationCannotUseFilesystemRules(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	cmd := `Remove-PSDrive C; New-PSDrive -Name C -PSProvider Registry -Root HKCU:\Software; Set-Content -Path C:\Users\marker.txt -Value x`
	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Equal(t, []string{"PowerShell path context"}, assessment.DynamicTargets)
	require.Empty(t, candidateRulesForIntent(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellProviderLocationCannotUseFilesystemRules(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	cmd := `Set-Location Env:; Set-Item .\MODS_REVIEW_PROBE value`
	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Equal(t, []string{"PowerShell path context"}, assessment.DynamicTargets)
	require.Empty(t, candidateRulesForIntent(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellPipelineProviderWriteCannotUseFilesystemRules(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	cmd := `Get-Item HKCU:\Software\Classes\Neovide | Set-ItemProperty -Name x -Value y`
	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
	require.Equal(t, []string{"PowerShell pipeline target"}, assessment.DynamicTargets)
	require.Empty(t, candidateRulesForIntent(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

func TestAssessCommandPowerShellRegistryLoopKeepsOnlyDynamicWriter(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	cmd := `$exts = @('.txt','.md','.json','.yaml','.yml','.ini','.conf','.log','.csv','.lua','.go','.js','.ts','.py','.c','.h','.cpp','.rs','.sh','.toml','.xml','.html','.css'); foreach ($e in $exts) { $p = "HKCU:\Software\Classes\$e\OpenWithProgids"; New-Item -Path $p -Force | Out-Null; New-ItemProperty -Path $p -Name "Neovide" -PropertyType None -Value ([byte[]]@()) -Force | Out-Null }; ($exts | ForEach-Object { if (Get-ItemProperty "HKCU:\Software\Classes\$_\OpenWithProgids" -Name Neovide -ErrorAction SilentlyContinue) { $_ } }).Count`

	assessment := m.assessCommand("powershell_run", cmd)

	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Equal(t, []string{`$p`}, assessment.DynamicTargets)
	require.Empty(t, assessment.ProviderWriteTargets)
	require.NotContains(t, strings.Join(assessment.DynamicTargets, "\n"), `$_`)
}

func TestAssessCommandPowerShellLiteralRegistryReadDoesNotBecomeWrite(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{Config: testConfigForWorkingDir(cwd)}
	assessment := m.assessCommand("powershell_run", `$p = "HKCU:\Software\Classes\Neovide"; Get-ItemProperty -Path $p`)

	require.Equal(t, approval.EffectRead, assessment.Effect)
	require.Empty(t, assessment.ProviderWriteTargets)
	require.Empty(t, assessment.DynamicTargets)
	require.Equal(t, DecisionAllow, ClassifyAccess(
		assessment.AccessIntent(),
		WorkingDirScope(cwd),
		nil,
		ApprovalReviewMode(ReviewAuto),
	))
}

// Native-program switches must not become filesystem targets.
func TestAssessShellCommandPowerShellSwitchesAreNotPaths(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	m := &Mods{
		Config: testConfigForWorkingDir(t.TempDir()),
	}

	got := m.assessCommand("powershell_run", `custom-writer /out src.cs`)

	require.Equal(t, approval.EffectUnknown, got.Effect)
	require.NotContains(t, got.KnownDirs, "/out")
	require.NotContains(t, got.KnownDirs, "/d")

}

func TestDynamicReadNeedsNoReviewOrRule(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	cmd := `Get-Content -LiteralPath $env:APP_NOTES_FILE`
	args := []byte(`{"command":` + strconv.Quote(cmd) + `}`)

	assessment := m.assessCommand("powershell_run", cmd)
	intent := assessment.AccessIntent()
	require.True(t, intent.HasUnresolvedPaths())
	require.Equal(t, DecisionAllow, ClassifyAccess(intent, WorkingDirScope(cwd), nil, ApprovalReviewMode(ReviewAuto)))

	scope := WorkingDirScope(cwd)
	require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: scope}
	require.NoError(t, reviewer.requestApproval(reviewerDeps{ctx: context.Background(), shellExecution: true, assessment: &assessment, accessIntent: intent}, "powershell_run", args))
}

func TestAssessCommandPowerShellEncodingConstructorIsNotADynamicTarget(t *testing.T) {
	t.Cleanup(func() { approval.CloseBridge() })

	cwd := t.TempDir()
	m := &Mods{
		Config: testConfigForWorkingDir(cwd),
	}
	// Regression: New-Object System.Text.UTF8Encoding($false) parses the
	// constructor argument as "($false)", which used to surface as a bogus
	// dynamic target and turn an ordinary encoding argument into a
	// DANGER "Modify a dynamic target" review.
	cmd := "$c = Get-Content -Raw 'plugins\\what-size.yazi\\main.lua'; $c = $c.Replace(\"`r`n\", \"`n\"); [System.IO.File]::WriteAllText((Resolve-Path 'plugins\\what-size.yazi\\main.lua'), $c, (New-Object System.Text.UTF8Encoding($false)))"

	assessment := m.assessCommand("powershell_run", cmd)

	require.Empty(t, assessment.DynamicTargets, "a boolean constructor argument is never a runtime path")
	require.Equal(t, "unknown effect and location", shellRiskLevel(assessment, WorkingDirScope(cwd)))
}
