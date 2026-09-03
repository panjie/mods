package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandAssessmentAccessIntent(t *testing.T) {
	readWithDir := CommandAssessment{Effect: EffectRead, KnownDirs: []string{"/etc"}, RemoteOrigins: []string{"https://api.example.com"}, UnresolvedRemoteTargets: []string{"$REMOTE"}, DynamicTargets: []string{"$PROFILE"}}
	require.Equal(t, AccessRead, readWithDir.AccessIntent().Class)
	require.Equal(t, []string{"/etc"}, readWithDir.AccessIntent().Dirs)
	require.Equal(t, []string{"https://api.example.com"}, readWithDir.AccessIntent().RemoteOrigins)
	require.Equal(t, []string{"$REMOTE"}, readWithDir.AccessIntent().UnresolvedRemoteTargets)
	require.Equal(t, []string{"$PROFILE"}, readWithDir.AccessIntent().UnresolvedPaths)

	dynamicRead := CommandAssessment{Effect: EffectRead, DynamicTargets: []string{"$PROFILE"}, DynamicProbe: true}
	require.Equal(t, AccessRead, dynamicRead.AccessIntent().Class)
	require.Empty(t, dynamicRead.AccessIntent().Dirs)
	require.Equal(t, []string{"$PROFILE"}, dynamicRead.AccessIntent().UnresolvedPaths)
	require.True(t, dynamicRead.AccessIntent().DynamicProbe)
	require.Equal(t, DecisionAllow, ClassifyAccess(dynamicRead.AccessIntent(), Scope{Value: "/workspace"}, nil, ReviewAuto))

	dynamicWrite := CommandAssessment{Effect: EffectWrite, DynamicTargets: []string{"$PROFILE"}}
	require.Equal(t, AccessWrite, dynamicWrite.AccessIntent().Class)
	require.Equal(t, []string{"$PROFILE"}, dynamicWrite.AccessIntent().UnresolvedPaths)
	require.Equal(t, AccessWrite, CommandAssessment{Effect: EffectUnknown}.AccessIntent().Class)
}

func TestAssessPOSIXDynamicTargetsUsePathContext(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		wantTargets []string
		wantEffect  CommandEffect
	}{
		{
			name: "loop scalars and assignment substitutions are not paths",
			command: `echo "=== lines ===" && for ext in go md; do count=$(find . -name "*.$ext" | wc -l); files=$(find . -name "*.$ext" | wc -l); ` +
				`[ "$count" -gt 0 ] 2>/dev/null && echo "$ext: $count lines, $files files"; done 2>/dev/null`,
			wantEffect: EffectUnknown,
		},
		{name: "plain scalar", command: `printf '%s\n' "$value"`, wantEffect: EffectRead},
		{name: "numeric test", command: `[ "$count" -gt 0 ]`, wantEffect: EffectRead},
		{name: "reader operand", command: `cat "$FILE"`, wantTargets: []string{"$FILE"}, wantEffect: EffectRead},
		{name: "find root but not pattern", command: `find "$ROOT" -name "*.$ext" -print`, wantTargets: []string{"$ROOT"}, wantEffect: EffectUnknown},
		{name: "input redirect", command: `wc -l < "$INPUT"`, wantTargets: []string{"$INPUT"}, wantEffect: EffectRead},
		{name: "path command substitution", command: `cat "$(resolve_path)"`, wantTargets: []string{"command substitution"}, wantEffect: EffectUnknown},
		{
			name:        "workspace file enumeration substitution remains unresolved",
			command:     `wc -l $(git ls-files '*.go' | grep -v '_test.go') | tail -1`,
			wantTargets: []string{"command substitution"},
			wantEffect:  EffectRead,
		},
		{
			name:        "formatted git output is not a bounded path stream",
			command:     `wc -l $(git ls-files '--format=/etc/passwd')`,
			wantTargets: []string{"command substitution"},
			wantEffect:  EffectRead,
		},
		{
			name:        "transforming filter is not a bounded path stream",
			command:     `wc -l $(git ls-files '*.go' | sed 's|.*|/etc/passwd|')`,
			wantTargets: []string{"command substitution"},
			wantEffect:  EffectUnknown,
		},
		{
			name:        "truncating filter can synthesize an absolute path",
			command:     `wc -l $(git ls-files | tail -c 12)`,
			wantTargets: []string{"command substitution"},
			wantEffect:  EffectRead,
		},
		{
			name:        "external git working directory is not bounded to workspace",
			command:     `wc -l $(git -C /etc ls-files '*.go')`,
			wantTargets: []string{"command substitution"},
			wantEffect:  EffectUnknown,
		},
		{name: "write redirect", command: `printf x > "$OUT"`, wantTargets: []string{"$OUT"}, wantEffect: EffectWrite},
		{name: "writer operand", command: `rm "$TARGET"`, wantTargets: []string{"$TARGET"}, wantEffect: EffectWrite},
		{name: "unknown command argument fails closed", command: `custom-tool "$value"`, wantTargets: []string{"$value"}, wantEffect: EffectUnknown},
		{name: "env option does not hide nested path", command: `env -u FOO cat "$FILE"`, wantTargets: []string{"$FILE"}, wantEffect: EffectUnknown},
		{name: "find path predicate stays dynamic", command: `find . -newer "$REFERENCE"`, wantTargets: []string{"$REFERENCE"}, wantEffect: EffectUnknown},
		{name: "xargs input file stays dynamic", command: `xargs -a "$LIST" wc -l`, wantTargets: []string{"$LIST"}, wantEffect: EffectUnknown},
		{name: "file test operand stays dynamic", command: `test -f "$FILE"`, wantTargets: []string{"$FILE"}, wantEffect: EffectRead},
		{name: "assignment result does not hide nested path", command: `count=$(cat "$FILE")`, wantTargets: []string{"$FILE"}, wantEffect: EffectUnknown},
		{name: "output argument does not hide nested path", command: `printf '%s' "$(cat "$FILE")"`, wantTargets: []string{"$FILE"}, wantEffect: EffectRead},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assessment := AssessShellStaticWithPolicy(tt.command, true, ReadOnlyCommandPolicy{})
			require.Equal(t, tt.wantTargets, assessment.DynamicTargets)
			require.Equal(t, tt.wantEffect, assessment.Effect)
		})
	}
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
