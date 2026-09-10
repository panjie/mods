package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandConstraintPOSIX(t *testing.T) {
	for _, command := range []string{
		`cd /tmp && mkdir -p mediainfo-new && cd mediainfo-new && for f in adobe.lua audio.lua const.lua; do curl -s -o "$f" "https://raw.githubusercontent.com"; done && ls -la`,
		`for f in a b; do touch "$f"; done`,
		`mkdir x && touch x/a`, `python3 -c 'open("x","w").write("y")'`,
		`env python3 -c 'print(1)'`, `sh /tmp/generated.sh`, `node -e 'require("fs").writeFileSync("x","y")'`,
		`eval "$COMMAND"`, `source /tmp/generated.sh`, `sh -c 'touch x'`,
		`{ mkdir x; touch x/a; }`, `"$COMMAND" --output x`,
		`env -u FOO python3 -c 'print(1)'`, `env -S 'python3 -c "print(1)"'`,
	} {
		t.Run(command, func(t *testing.T) {
			a := AssessShellStaticWithPolicy(command, true, ReadOnlyCommandPolicy{})
			require.True(t, a.RequiresSimplification())
			a.Effect = EffectRead // model fallback cannot lift structural rejection
			require.True(t, a.RequiresSimplification())
		})
	}
	for _, command := range []string{`git status`, `ls -la | head -n 5`, `printf '%s\n' 'literal;semicolon'`, `go test ./...`, `mkdir output`} {
		t.Run(command, func(t *testing.T) {
			require.False(t, AssessShellStaticWithPolicy(command, true, ReadOnlyCommandPolicy{}).RequiresSimplification())
		})
	}
}

func TestCommandConstraintInterpreterArgv(t *testing.T) {
	for _, posix := range []bool{true, false} {
		for _, tc := range []struct {
			program string
			args    []string
		}{
			{"python3", []string{"-c", "print(1)"}}, {"node", []string{"-e", "console.log(1)"}},
			{"sh", []string{"/tmp/generated.sh"}}, {"powershell.exe", []string{"-EncodedCommand", "ZQB4AGkAdAA="}},
			{"pwsh", []string{"-File", "script.ps1"}}, {"emacs", []string{"--batch", "--eval", "(message \"x\")"}},
			{"script.py", nil}, {"env", []string{"X=y", "python3", "script.py"}},
		} {
			a := AssessArgvStaticWithPolicy(tc.program, tc.args, posix, ReadOnlyCommandPolicy{})
			require.True(t, a.RequiresSimplification(), "%s %v posix=%v", tc.program, tc.args, posix)
		}
	}
}

func TestCommandConstraintPowerShellIR(t *testing.T) {
	for _, ir := range []*psBridgeIR{
		{HasControlFlow: true, Invocations: []psCommandInvocation{{Name: "Set-Content", Args: []string{"$f", "x"}}}},
		{HasScriptBlock: true, Invocations: []psCommandInvocation{{Name: "ForEach-Object"}, {Name: "Set-Content"}}},
		{TopLevelStatementCount: 3, Invocations: []psCommandInvocation{{Name: "New-Item"}, {Name: "Invoke-WebRequest"}, {Name: "Get-ChildItem"}}},
	} {
		a := assessPowerShellIR("", ir, ReadOnlyCommandPolicy{}, "")
		require.True(t, a.RequiresSimplification())
	}
}
