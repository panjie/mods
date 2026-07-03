//go:build windows

package approval

import (
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
