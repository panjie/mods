package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBlankPowerShellHereStrings(t *testing.T) {
	command := "$p=\"C:\\x\\init.el\"\n$new=@'\n$p=\"fake\"\nline two\n'@\nSet-Content $p"
	blanked := blankPowerShellHereStrings(command)
	require.NotContains(t, blanked, "fake")
	require.Contains(t, blanked, "$p=\"C:\\x\\init.el\"", "real assignment outside the here-string must survive")
	require.Contains(t, blanked, "\nSet-Content $p", "statement after the here-string must survive")
	require.Equal(t, len(command), len(blanked), "blanking must preserve length/newlines")
}

func TestPowerShellLiteralAssignments(t *testing.T) {
	tests := []struct {
		name    string
		command string
		ir      *psBridgeIR
		want    map[string]string
	}{
		{
			name:    "single literal assignment",
			command: `$p="C:\Users\x\init.el"; Set-Content -Path $p -Value y`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`}},
			want:    map[string]string{"p": `C:\Users\x\init.el`},
		},
		{
			name:    "here-string content does not fake an assignment",
			command: "$p=\"C:\\x\\init.el\"\n$x=@'\n$p=\"C:\\fake\\path\"\n'@\nSet-Content $p",
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`, `$x`}},
			want:    map[string]string{"p": `C:\x\init.el`},
		},
		{
			name:    "multiple assignments are omitted",
			command: `$p="C:\a"; $p="C:\b"; Set-Content $p`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`, `$p`}},
			want:    map[string]string{},
		},
		{
			name:    "interpolated value is omitted",
			command: `$p="C:\Users\$name\init.el"; Set-Content $p`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`}},
			want:    map[string]string{},
		},
		{
			name:    "compound assignment is omitted",
			command: `$p += "C:\x"; Set-Content $p`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`}},
			want:    map[string]string{},
		},
		{
			name:    "embedded quote is omitted",
			command: `$x='a''b'; Get-Content $x`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$x`}},
			want:    map[string]string{},
		},
		{
			name:    "script-block assignment is omitted",
			command: "foreach ($f in (Get-ChildItem)) {\n$p = \"C:\\x\"\nSet-Content $p\n}",
			ir:      &psBridgeIR{ScriptBlockAssignmentTargets: []string{`$p`}},
			want:    map[string]string{},
		},
		{
			name:    "braced reference is supported",
			command: `${p}="C:\Users\x\init.el"; Get-Content ${p}`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`${p}`}},
			want:    map[string]string{"p": `C:\Users\x\init.el`},
		},
		{
			name:    "non-literal value is omitted",
			command: `$p = Join-Path $HOME "x"; Set-Content $p`,
			ir:      &psBridgeIR{AssignmentTargets: []string{`$p`}},
			want:    map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := powerShellLiteralAssignments(tt.command, tt.ir)
			if len(tt.want) == 0 {
				require.Empty(t, got)
				return
			}
			require.Equal(t, tt.want, got)
		})
	}

	require.Nil(t, powerShellLiteralAssignments("cmd", nil))
	require.Nil(t, powerShellLiteralAssignments("cmd", &psBridgeIR{}))
}

func TestSafePowerShellAssignmentsLiteral(t *testing.T) {
	require.True(t, safePowerShellAssignments(`Get-Content x`, &psBridgeIR{}),
		"no assignment is trivially safe")

	ir := &psBridgeIR{HasAssignment: true, AssignmentTargets: []string{`$p`}}
	require.True(t, safePowerShellAssignments(`$p = "C:\x"; Get-Content $p`, ir),
		"a pure-literal top-level assignment is inert")

	ir = &psBridgeIR{HasAssignment: true, AssignmentTargets: []string{`$p`}}
	require.False(t, safePowerShellAssignments(`$p = Get-Content x; Get-Content $p`, ir),
		"a non-literal top-level assignment stays unsafe")

	ir = &psBridgeIR{HasAssignment: true, AssignmentTargets: []string{`$p`}}
	require.False(t, safePowerShellAssignments(`$p = "C:\$x"; Get-Content $p`, ir),
		"an interpolated assignment stays unsafe")

	ir = &psBridgeIR{HasAssignment: true, HasScriptBlock: true,
		AssignmentTargets: []string{`$sum`}, ScriptBlockAssignmentTargets: []string{`$sum`}}
	require.True(t, safePowerShellAssignments(`$sum = $start; Get-ChildItem | ForEach-Object { $sum += $_.Length }`, ir),
		"the script-block accumulator pattern still passes")
}
