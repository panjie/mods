package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func hasScriptExecutionReason(reasons []ReviewabilityReason) bool {
	for _, reason := range reasons {
		if reason == ReviewabilityScriptExecution {
			return true
		}
	}
	return false
}

func TestScriptExecutionArgvEligibleShapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program string
		args    []string
		posix   bool
		want    string
	}{
		{name: "posix python3", program: "python3", args: []string{"tools/check.py"}, posix: true, want: "python3"},
		{name: "windows python", program: "python", args: []string{"check.py"}, posix: false, want: "python"},
		{name: "versioned python", program: "python3.12", args: []string{"check.py"}, posix: true, want: "python3.12"},
		{name: "windows launcher", program: "py", args: []string{"check.py"}, posix: false, want: "py"},
		{name: "node", program: "node", args: []string{"server.js"}, posix: true, want: "node"},
		{name: "upper case php", program: "PHP", args: []string{"index.php"}, posix: true, want: "php"},
		{name: "pypy", program: "pypy3", args: []string{"check.py"}, posix: true, want: "pypy3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := AssessArgvStaticWithPolicy(tc.program, tc.args, tc.posix, ReadOnlyCommandPolicy{})
			facts := a.Reviewability.ScriptExecution
			require.NotNil(t, facts)
			require.Equal(t, tc.want, facts.Interpreter)
			require.Equal(t, tc.args[0], facts.Operand)
			require.False(t, facts.Verified(), "the approval layer must not verify script bytes on its own")
			require.Equal(t, ReviewabilityOpaque, a.Reviewability.Level)
			require.True(t, hasScriptExecutionReason(a.Reviewability.Reasons))
			require.False(t, a.Reviewability.ShouldCorrect, "script execution is already the form the reviewer can read")
			require.True(t, a.RequiresSimplification(), "only the app layer may release a verified script call")
			require.False(t, a.StaticRead)
			require.NotEqual(t, EffectRead, a.Effect, "an interpreter script is never a proven read")
		})
	}
}

func TestScriptExecutionIneligibleShapes(t *testing.T) {
	for _, tc := range []struct {
		name    string
		program string
		args    []string
	}{
		{name: "inline code flag", program: "python3", args: []string{"-c", "print(1)"}},
		{name: "module flag", program: "python3", args: []string{"-m", "pytest"}},
		{name: "interpreter flag before the script", program: "python3", args: []string{"-u", "check.py"}},
		{name: "extra argument", program: "python3", args: []string{"check.py", "extra"}},
		{name: "runtime path variable", program: "python3", args: []string{"$SCRIPT"}},
		{name: "glob operand", program: "python3", args: []string{"*.py"}},
		{name: "stdin marker", program: "python3", args: []string{"-"}},
		{name: "quoted operand", program: "python3", args: []string{`'check.py'`}},
		{name: "absolute interpreter path", program: "/usr/bin/python3", args: []string{"check.py"}},
		{name: "relative interpreter path", program: "./venv/bin/python", args: []string{"check.py"}},
		{name: "posix shell host", program: "sh", args: []string{"check.sh"}},
		{name: "bash host", program: "bash", args: []string{"check.sh"}},
		{name: "powershell host", program: "pwsh", args: []string{"-File", "check.ps1"}},
		{name: "script executed directly", program: "check.py", args: nil},
		{name: "test runner", program: "pytest", args: []string{"tests/"}},
		{name: "build tool", program: "go", args: []string{"run", "main.go"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := AssessArgvStaticWithPolicy(tc.program, tc.args, true, ReadOnlyCommandPolicy{})
			require.Nil(t, a.Reviewability.ScriptExecution)
		})
	}
}

func TestScriptExecutionPOSIXShellShape(t *testing.T) {
	for _, command := range []string{
		"python3 tools/check.py",
		"python check.py",
		"node server.js",
	} {
		t.Run(command, func(t *testing.T) {
			a := AssessShellStaticWithPolicy(command, true, ReadOnlyCommandPolicy{})
			require.NotNil(t, a.Reviewability.ScriptExecution)
			require.True(t, a.Shape.Opaque)
			require.Equal(t, 1, a.Shape.TopLevelActions)
			require.True(t, a.RequiresSimplification())
			require.False(t, a.StaticRead)
		})
	}

	for _, command := range []string{
		"python3 -c 'print(1)'",
		"python3 -u check.py",
		"python3 check.py extra",
		"FOO=1 python3 check.py",
		"python3 check.py > out.txt",
		"python3 check.py | tee log",
		"python3 a.py && python3 b.py",
		"python3 a.py; python3 b.py",
		"python3 check.py &",
		"! python3 check.py",
		"sh check.sh",
		"bash check.sh",
		"python3 $SCRIPT",
		"python3 *.py",
		"python3 ~/check.py",
		"python3 `echo check.py`",
		"python3 $(echo check.py)",
	} {
		t.Run("ineligible "+command, func(t *testing.T) {
			require.Nil(t, AssessShellStaticWithPolicy(command, true, ReadOnlyCommandPolicy{}).Reviewability.ScriptExecution)
		})
	}
}

func TestScriptExecutionPowerShellIRShape(t *testing.T) {
	base := func() *psBridgeIR {
		return &psBridgeIR{
			Invocations:            []psCommandInvocation{{Name: "python", Args: []string{"check.py"}}},
			TopLevelStatementCount: 1,
			PipelineCount:          1,
		}
	}
	a := assessPowerShellIR("python check.py", base(), ReadOnlyCommandPolicy{}, "")
	require.NotNil(t, a.Reviewability.ScriptExecution)
	require.Equal(t, "python", a.Reviewability.ScriptExecution.Interpreter)
	require.Equal(t, "check.py", a.Reviewability.ScriptExecution.Operand)
	require.True(t, a.RequiresSimplification())

	for _, tc := range []struct {
		name   string
		mutate func(*psBridgeIR)
	}{
		{name: "inline code flag", mutate: func(ir *psBridgeIR) { ir.Invocations[0].Args = []string{"-c", "print(1)"} }},
		{name: "extra argument", mutate: func(ir *psBridgeIR) { ir.Invocations[0].Args = []string{"check.py", "extra"} }},
		{name: "script block", mutate: func(ir *psBridgeIR) { ir.HasScriptBlock = true }},
		{name: "control flow", mutate: func(ir *psBridgeIR) { ir.HasControlFlow = true }},
		{name: "assignment", mutate: func(ir *psBridgeIR) { ir.HasAssignment = true }},
		{name: "background", mutate: func(ir *psBridgeIR) { ir.HasBackground = true }},
		{name: "stop parsing", mutate: func(ir *psBridgeIR) { ir.HasStopParsing = true }},
		{name: "redirect", mutate: func(ir *psBridgeIR) { ir.Redirects = []string{">"} }},
		{name: "expansion", mutate: func(ir *psBridgeIR) { ir.Expansions = []string{"$x"} }},
		{name: "two statements", mutate: func(ir *psBridgeIR) { ir.TopLevelStatementCount = 2 }},
		{name: "two pipelines", mutate: func(ir *psBridgeIR) { ir.PipelineCount = 2 }},
		{name: "invoke expression", mutate: func(ir *psBridgeIR) { ir.RiskFlags = []string{"invoke_expression"} }},
		{name: "nested shell host", mutate: func(ir *psBridgeIR) {
			ir.Invocations[0] = psCommandInvocation{Name: "pwsh", Args: []string{"-File", "check.ps1"}}
		}},
		{name: "two invocations", mutate: func(ir *psBridgeIR) {
			ir.Invocations = append(ir.Invocations, psCommandInvocation{Name: "Get-Item", Args: []string{"check.py"}})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ir := base()
			tc.mutate(ir)
			got := assessPowerShellIR("python check.py", ir, ReadOnlyCommandPolicy{}, "")
			require.Nil(t, got.Reviewability.ScriptExecution)
		})
	}
}
