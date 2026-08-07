package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandAssessmentAccessIntent(t *testing.T) {
	read := CommandAssessment{Effect: EffectRead, KnownDirs: []string{"/etc"}, DynamicTargets: []string{"$PROFILE"}}
	require.Equal(t, AccessRead, read.AccessIntent().Class)
	require.Equal(t, []string{"/etc"}, read.AccessIntent().Dirs)
	require.Equal(t, []string{"$PROFILE"}, read.AccessIntent().UnresolvedPaths)

	require.Equal(t, AccessWrite, CommandAssessment{Effect: EffectWrite}.AccessIntent().Class)
	require.Equal(t, AccessWrite, CommandAssessment{Effect: EffectUnknown}.AccessIntent().Class)
}

func TestAssessPowerShellIRProfileAndEnvironmentReads(t *testing.T) {
	tests := []struct {
		name       string
		command    string
		ir         *psBridgeIR
		target     string
		compound   bool
		correction bool
	}{
		{
			name:    "profile member then test path",
			command: `$PROFILE.CurrentUserCurrentHost; Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost`,
			ir: &psBridgeIR{
				Commands:                 []string{"test-path"},
				Variables:                []string{"PROFILE"},
				Expansions:               []string{"var"},
				MemberExpressions:        []string{`$PROFILE.CurrentUserCurrentHost`},
				TopLevelValueExpressions: []string{`$PROFILE.CurrentUserCurrentHost`},
				Invocations:              []psCommandInvocation{{Name: "test-path", Args: []string{"-LiteralPath", `$PROFILE.CurrentUserCurrentHost`}}},
				TopLevelStatementCount:   2,
				PipelineCount:            2,
			},
			target:     `$PROFILE.CurrentUserCurrentHost`,
			compound:   true,
			correction: true,
		},
		{
			name:    "environment value then test path",
			command: `$env:STARSHIP_CONFIG; Test-Path $env:STARSHIP_CONFIG`,
			ir: &psBridgeIR{
				Commands:                 []string{"test-path"},
				Variables:                []string{"env:STARSHIP_CONFIG"},
				Expansions:               []string{"var"},
				TopLevelValueExpressions: []string{`$env:STARSHIP_CONFIG`},
				Invocations:              []psCommandInvocation{{Name: "test-path", Args: []string{`$env:STARSHIP_CONFIG`}}},
				TopLevelStatementCount:   2,
				PipelineCount:            2,
			},
			target:     `$env:STARSHIP_CONFIG`,
			compound:   true,
			correction: true,
		},
		{
			name:    "single profile probe",
			command: `Test-Path -LiteralPath $PROFILE.CurrentUserCurrentHost`,
			ir: &psBridgeIR{
				Commands:               []string{"test-path"},
				Variables:              []string{"PROFILE"},
				Expansions:             []string{"var"},
				MemberExpressions:      []string{`$PROFILE.CurrentUserCurrentHost`},
				Invocations:            []psCommandInvocation{{Name: "test-path", Args: []string{"-LiteralPath", `$PROFILE.CurrentUserCurrentHost`}}},
				TopLevelStatementCount: 1,
				PipelineCount:          1,
			},
			target: `$PROFILE.CurrentUserCurrentHost`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := assessPowerShellIR(tc.command, tc.ir, ReadOnlyCommandPolicy{})
			require.Equal(t, EffectRead, assessment.Effect)
			require.Contains(t, assessment.DynamicTargets, tc.target)
			require.Empty(t, assessment.KnownDirs)
			require.Equal(t, AccessRead, assessment.AccessIntent().Class)
			require.Equal(t, []string{tc.target}, assessment.AccessIntent().UnresolvedPaths)
			if tc.compound {
				require.Equal(t, ReviewabilityCompound, assessment.Reviewability.Level)
			} else {
				require.Equal(t, ReviewabilitySimple, assessment.Reviewability.Level)
			}
			require.Equal(t, tc.correction, assessment.Reviewability.ShouldCorrect)
		})
	}
}

func TestAssessPowerShellIRRejectsUnsafeDynamicExpressions(t *testing.T) {
	tests := []struct {
		name    string
		command string
		ir      *psBridgeIR
	}{
		{
			name:    "arbitrary member",
			command: `Write-Output $PROFILE.PSObject.Properties`,
			ir:      &psBridgeIR{Commands: []string{"write-output"}, Variables: []string{"PROFILE"}, MemberExpressions: []string{`$PROFILE.PSObject`, `$PROFILE.PSObject.Properties`}, Invocations: []psCommandInvocation{{Name: "write-output", Args: []string{`$PROFILE.PSObject.Properties`}}}},
		},
		{
			name:    "environment member",
			command: `Write-Output $env:STARSHIP_CONFIG.Length`,
			ir:      &psBridgeIR{Commands: []string{"write-output"}, Variables: []string{"env:STARSHIP_CONFIG"}, MemberExpressions: []string{`$env:STARSHIP_CONFIG.Length`}, Invocations: []psCommandInvocation{{Name: "write-output", Args: []string{`$env:STARSHIP_CONFIG.Length`}}}},
		},
		{
			name:    "arbitrary variable",
			command: `Test-Path $target`,
			ir:      &psBridgeIR{Commands: []string{"test-path"}, Variables: []string{"target"}, Invocations: []psCommandInvocation{{Name: "test-path", Args: []string{`$target`}}}},
		},
		{
			name:    "top-level assignment",
			command: `$target = $PROFILE`,
			ir:      &psBridgeIR{HasAssignment: true, Variables: []string{"target", "PROFILE"}, AssignmentTargets: []string{"$target"}},
		},
		{
			name:    "method call",
			command: `Write-Output $PROFILE.ToString()`,
			ir:      &psBridgeIR{Commands: []string{"write-output"}, Variables: []string{"PROFILE"}, MethodInvocations: []string{"ToString"}, MemberExpressions: []string{`$PROFILE.ToString()`}, Invocations: []psCommandInvocation{{Name: "write-output", Args: []string{`$PROFILE.ToString()`}}}},
		},
		{
			name:    "static member",
			command: `[Environment]::UserName`,
			ir:      &psBridgeIR{StaticMembers: []string{`[Environment]::UserName`}, TopLevelValueExpressions: []string{`[Environment]::UserName`}},
		},
		{
			name:    "invoke expression",
			command: `Invoke-Expression $env:STARSHIP_CONFIG`,
			ir:      &psBridgeIR{Commands: []string{"invoke-expression"}, Variables: []string{"env:STARSHIP_CONFIG"}, RiskFlags: []string{"invoke_expression"}, Invocations: []psCommandInvocation{{Name: "invoke-expression", Args: []string{`$env:STARSHIP_CONFIG`}}}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := assessPowerShellIR(tc.command, tc.ir, ReadOnlyCommandPolicy{})
			require.Equal(t, EffectUnknown, assessment.Effect)
			require.Equal(t, AccessWrite, assessment.AccessIntent().Class)
		})
	}
}
