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
			got := AnalyzeCommandReviewability(tt.command, true, ReadOnlyCommandPolicy{})
			require.Equal(t, tt.level, got.Level)
			require.Equal(t, tt.correct, got.ShouldCorrect)
			require.Equal(t, tt.recommended, got.RecommendedTool)
			if tt.reason != "" {
				require.Contains(t, got.Reasons, tt.reason)
			}
			if tt.statements > 0 {
				require.Equal(t, tt.statements, got.TopLevelStatements)
			}
			require.Equal(t, tt.pipelineCount, got.PipelineCount)
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
		got := AnalyzeCommandReviewability(cmd, false, ReadOnlyCommandPolicy{})
		require.Equal(t, ReviewabilityCompound, got.Level)
		require.True(t, got.ShouldCorrect)
		require.GreaterOrEqual(t, got.TopLevelStatements, 4)
		require.Contains(t, got.DynamicTargets, `$v`)
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
}

func TestLooksLikePowerShellCmdlet(t *testing.T) {
	require.True(t, looksLikePowerShellCmdlet("Set-ExecutionPolicy"))
	require.True(t, looksLikePowerShellCmdlet("Get-ChildItem"))
	require.False(t, looksLikePowerShellCmdlet("winget"))
	require.False(t, looksLikePowerShellCmdlet("my-report"))
}
