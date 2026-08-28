package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellLiteralAssignments(t *testing.T) {
	tests := []struct {
		name string
		ir   *psBridgeIR
		want map[string]string
	}{
		{
			name: "single literal assignment",
			ir: &psBridgeIR{
				AssignmentTargets:  []string{`$p`},
				LiteralAssignments: map[string]string{"p": `C:\Users\x\init.el`},
			},
			want: map[string]string{"p": `C:\Users\x\init.el`},
		},
		{
			name: "non-literal assignment has no AST candidate",
			ir: &psBridgeIR{
				AssignmentTargets: []string{`$p`},
			},
			want: map[string]string{},
		},
		{
			name: "multiple assignments are omitted even with a candidate",
			ir: &psBridgeIR{
				AssignmentTargets:  []string{`$p`, `$p`},
				LiteralAssignments: map[string]string{"p": `C:\b`},
			},
			want: map[string]string{},
		},
		{
			name: "script-block assignment is omitted",
			ir: &psBridgeIR{
				AssignmentTargets:            []string{`$p`},
				ScriptBlockAssignmentTargets: []string{`$p`},
				LiteralAssignments:           map[string]string{"p": `C:\x`},
			},
			want: map[string]string{},
		},
		{
			name: "braced reference is supported",
			ir: &psBridgeIR{
				AssignmentTargets:  []string{`${p}`},
				LiteralAssignments: map[string]string{"p": `C:\Users\x\init.el`},
			},
			want: map[string]string{"p": `C:\Users\x\init.el`},
		},
		{
			name: "AST-decoded embedded quote remains a literal",
			ir: &psBridgeIR{
				AssignmentTargets:  []string{`$x`},
				LiteralAssignments: map[string]string{"x": `a'b`},
			},
			want: map[string]string{"x": `a'b`},
		},
		{
			name: "non-local variable is omitted",
			ir: &psBridgeIR{
				AssignmentTargets:  []string{`$env:TEMP`},
				LiteralAssignments: map[string]string{"env:TEMP": `C:\x`},
			},
			want: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := powerShellLiteralAssignments(tt.ir)
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}

	require.Nil(t, powerShellLiteralAssignments(nil))
	require.Nil(t, powerShellLiteralAssignments(&psBridgeIR{}))
}

func TestSafePowerShellAssignmentsLiteral(t *testing.T) {
	require.True(t, safePowerShellAssignments(&psBridgeIR{}),
		"no assignment is trivially safe")

	ir := &psBridgeIR{
		HasAssignment:      true,
		AssignmentTargets:  []string{`$p`},
		LiteralAssignments: map[string]string{"p": `C:\x`},
	}
	require.True(t, safePowerShellAssignments(ir),
		"a pure-literal top-level assignment is inert")

	ir = &psBridgeIR{HasAssignment: true, AssignmentTargets: []string{`$p`}}
	require.False(t, safePowerShellAssignments(ir),
		"a non-literal top-level assignment stays unsafe")

	ir = &psBridgeIR{HasAssignment: true, AssignmentTargets: []string{`$p`}}
	require.False(t, safePowerShellAssignments(ir),
		"an interpolated assignment stays unsafe")

	ir = &psBridgeIR{HasAssignment: true, HasScriptBlock: true,
		AssignmentTargets: []string{`$sum`}, ScriptBlockAssignmentTargets: []string{`$sum`}}
	require.True(t, safePowerShellAssignments(ir),
		"the script-block accumulator pattern still passes")
}
