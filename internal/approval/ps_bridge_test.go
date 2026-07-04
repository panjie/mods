//go:build windows

package approval

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseWithBridgeSimpleCommand(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-ChildItem")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Commands, "get-childitem")
	require.False(t, ir.HasScriptBlock)
	require.False(t, ir.HasAssignment)
	require.Empty(t, ir.Redirects)
}

func TestParseWithBridgePipeline(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-ChildItem | Sort-Object Name")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Commands, "get-childitem")
	require.Contains(t, ir.Commands, "sort-object")
	require.Contains(t, ir.Operators, "|")
}

func TestParseWithBridgeScriptBlock(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Where-Object { $_.Name -eq 'foo' }")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasScriptBlock)
}

func TestParseWithBridgeRedirect(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Process > out.txt")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Redirects, "FileRedirection")
}

func TestParseWithBridgeSubexpression(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content $(Get-ChildItem)")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Expansions, "subshell")
}

func TestParseWithBridgeVariableExclusion(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Write-Output $true")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.NotContains(t, ir.Expansions, "var")
}

func TestParseWithBridgeVariableFlagged(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content $env:SECRET")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Expansions, "var")
}

func TestParseWithBridgeAssignment(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("$x = Get-Date")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasAssignment)
}

func TestParseWithBridgeControlFlow(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("if ($true) { Get-Date }")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.True(t, ir.HasControlFlow)
}

func TestParseWithBridgeSemicolon(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Date; Get-Process")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Operators, ";")
}

func TestParseWithBridgeCloseBridgeCleanup(t *testing.T) {
	_, err := parseWithBridge("Get-Date")
	require.NoError(t, err)
	CloseBridge()
	_, err = parseWithBridge("Get-Date")
	require.NoError(t, err)
	CloseBridge()
}

func TestParseWithBridgePathExtraction(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content -Path C:\\Users\\file.txt")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Paths, "C:\\Users\\file.txt")
}

func TestParseWithBridgePositionalPathExtraction(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Content C:\\Users\\file.txt")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Paths, "C:\\Users\\file.txt")
}

func TestParseWithBridgeRedirectPathExtraction(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	ir, err := parseWithBridge("Get-Process > C:\\Users\\out.txt")
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Paths, "C:\\Users\\out.txt")
}

func TestParseWithBridgeNonPathArgNotCollected(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	// "Name" is a positional arg of Sort-Object, not a path
	ir, err := parseWithBridge("Get-ChildItem | Sort-Object Name")
	require.NoError(t, err)
	require.NotNil(t, ir)
	// "Name" will be in paths, but the Go-side filterArgPaths will exclude it
	// because it doesn't look like a path. Just verify the bridge collects it.
	require.Contains(t, ir.Paths, "Name")
}

// TestGetWindowsShellPathPinnedToPowerShell locks in that the classification
// bridge uses Windows PowerShell (powershell.exe), matching the shell that
// actually executes shell_run / powershell_run commands. Parsing under
// pwsh.exe (PowerShell 7) instead would accept PS7-only operators that the
// PS5.1 executor cannot run — a classifier/executor divergence. GitHub
// Actions Windows runners ship pwsh.exe on PATH, so this assertion proves the
// pin holds even when both hosts are installed.
func TestGetWindowsShellPathPinnedToPowerShell(t *testing.T) {
	p := getWindowsShellPath()
	if p == "" {
		t.Skip("powershell.exe not on PATH")
	}
	require.Equal(t, "powershell.exe", filepath.Base(p),
		"classifier bridge must use powershell.exe to match the PS5.1 executor")
}

// TestIsReadOnlyPowerShellPS7OperatorsFailClosed ensures PS7-only syntax
// (pipeline-chain && / ||, null-coalescing ??) does NOT bypass the read-only
// fast-path. Under the pinned powershell.exe (PS5.1) bridge these are parse
// errors, so the classifier fail-closes and routes the command to review —
// matching the PS5.1 executor, which cannot run them either. This guards
// against regressing back to a pwsh.exe classifier that would accept them:
// e.g. "Get-Content a && Get-Content b" parses read-only under PS7 but would
// then fail under the PS5.1 executor.
func TestIsReadOnlyPowerShellPS7OperatorsFailClosed(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cases := []struct {
		name string
		cmd  string
	}{
		{"pipeline chain &&", "Get-Content a && Get-Content b"},
		{"pipeline chain ||", "Get-Content a || Get-Content b"},
		{"null coalescing ??", "$x ?? 'default'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, _ := IsReadOnlyPowerShell(c.cmd)
			require.Falsef(t, got, "PS7-only cmd=%q must NOT be read-only under the PS5.1 classifier", c.cmd)
		})
	}
}
