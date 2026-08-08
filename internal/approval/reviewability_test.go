package approval

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzePOSIXCommandReviewability(t *testing.T) {
	tests := []struct {
		name          string
		command       string
		level         ReviewabilityLevel
		correct       bool
		recommended   string
		reason        ReviewabilityReason
		statements    int
		pipelineCount int
	}{
		{name: "single executable", command: "git status", level: ReviewabilitySimple, correct: true, recommended: "process_run", reason: ReviewabilitySingleProgramInShell, statements: 1},
		{name: "pipeline remains one purpose", command: "find . -name '*.go' | wc -l", level: ReviewabilitySimple, correct: false, statements: 1, pipelineCount: 1},
		{name: "shell builtin stays shell", command: "printf '%s\\n' hello", level: ReviewabilitySimple, correct: false, statements: 1},
		{name: "quoted semicolon", command: "printf '%s\\n' 'a;b'", level: ReviewabilitySimple, correct: false, statements: 1},
		{name: "three inspections", command: "echo first; git status; git diff", level: ReviewabilityCompound, correct: true, reason: ReviewabilityMultipleIndependent, statements: 3},
		{name: "mixed read and write", command: "git status; rm out.txt", level: ReviewabilityCompound, correct: true, reason: ReviewabilityMixedReadWrite, statements: 2},
		{name: "opaque syntax", command: "if then", level: ReviewabilityOpaque, correct: false, reason: ReviewabilityOpaqueExecution},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessment := AssessShellStaticWithPolicy(tt.command, true, ReadOnlyCommandPolicy{})
			got := assessment.Reviewability
			require.Equal(t, tt.level, got.Level)
			require.Equal(t, tt.correct, got.ShouldCorrect)
			require.Equal(t, tt.recommended, got.RecommendedTool)
			if tt.reason != "" {
				require.Contains(t, got.Reasons, tt.reason)
			}
			if tt.statements > 0 {
				require.Equal(t, tt.statements, assessment.Shape.TopLevelActions)
			}
			require.Equal(t, tt.pipelineCount, assessment.Shape.Pipelines)
		})
	}
}

func TestAnalyzePowerShellCommandReviewability(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell bridge requires Windows")
	}
	t.Cleanup(CloseBridge)

	t.Run("single native executable", func(t *testing.T) {
		got := AnalyzeCommandReviewability(`winget install --id Starship.Starship -e`, false, ReadOnlyCommandPolicy{})
		require.Equal(t, ReviewabilitySimple, got.Level)
		require.True(t, got.ShouldCorrect)
		require.Equal(t, "process_run", got.RecommendedTool)
		require.Contains(t, got.Reasons, ReviewabilitySingleProgramInShell)
	})

	t.Run("profile inspection example", func(t *testing.T) {
		cmd := `Write-Output "=== All profile paths ==="; $PROFILE | Format-List *; Write-Output "=== Which exist? ==="; $PROFILE.PSObject.Properties | ForEach-Object { $v = $_.Value; "{0,-45} exists={1}" -f $v, (Test-Path $v) }; Get-ChildItem "C:\Users\panjie\AppData\Local\Microsoft\WinGet\Links"`
		assessment := AssessShellStaticWithPolicy(cmd, false, ReadOnlyCommandPolicy{})
		got := assessment.Reviewability
		require.Equal(t, ReviewabilityCompound, got.Level)
		require.True(t, got.ShouldCorrect)
		require.GreaterOrEqual(t, assessment.Shape.TopLevelActions, 4)
		require.Contains(t, assessment.DynamicTargets, `$v`)
		require.Contains(t, got.Reasons, ReviewabilityDecorativeOutput)
	})
}

func TestAnalyzeProcessReviewability(t *testing.T) {
	require.Equal(t, ReviewabilitySimple, AnalyzeProcessReviewability("git", []string{"status"}, true).Level)

	posix := AnalyzeProcessReviewability("/bin/sh", []string{"-c", "git status"}, true)
	require.True(t, posix.ShouldCorrect)
	require.Equal(t, "shell_run", posix.RecommendedTool)
	require.Contains(t, posix.Reasons, ReviewabilityOpaqueExecution)

	powershell := AnalyzeProcessReviewability(`C:\Program Files\PowerShell\7\pwsh.exe`, []string{"-NoProfile", "-Command", "Get-Date"}, false)
	require.True(t, powershell.ShouldCorrect)
	require.Equal(t, "powershell_run", powershell.RecommendedTool)
	require.Equal(t, ReviewabilitySimple, AnalyzeProcessReviewability("pwsh", []string{"-Command", "Get-Date"}, true).Level)

	implicit := AnalyzeProcessReviewability("powershell.exe", []string{"Get-Content", "internal/tools/windows_reliability_test.go", "-TotalCount", "25"}, false)
	require.True(t, implicit.ShouldCorrect)
	require.Equal(t, "powershell_run", implicit.RecommendedTool)
	require.Contains(t, implicit.Reasons, ReviewabilityOpaqueExecution)

	implicitPwsh := AnalyzeProcessReviewability(`C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe`, []string{"head", "-1", "file.txt"}, false)
	require.True(t, implicitPwsh.ShouldCorrect)
	require.Equal(t, "powershell_run", implicitPwsh.RecommendedTool)

	require.Equal(t, ReviewabilitySimple, AnalyzeProcessReviewability("powershell.exe", nil, false).Level)
	require.Equal(t, ReviewabilitySimple, AnalyzeProcessReviewability("powershell.exe", []string{"-File", "script.ps1"}, false).Level)
	require.Equal(t, ReviewabilitySimple, AnalyzeProcessReviewability("powershell.exe", []string{"-NoProfile"}, false).Level)
}

func TestLooksLikePowerShellCmdlet(t *testing.T) {
	require.True(t, looksLikePowerShellCmdlet("Set-ExecutionPolicy"))
	require.True(t, looksLikePowerShellCmdlet("Get-ChildItem"))
	require.False(t, looksLikePowerShellCmdlet("winget"))
	require.False(t, looksLikePowerShellCmdlet("my-report"))
}
