//go:build windows

package approval

import (
	"os/exec"
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

func TestParseWithBridgeCommandInvocations(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	docPath := "docs/superpowers/plans/2026-07-02-unified-directory-approval.md"
	ir, err := parseWithBridge("git log --oneline -1 -- " + docPath)
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Contains(t, ir.Commands, "git")
	require.Len(t, ir.Invocations, 1)
	require.Equal(t, "git", ir.Invocations[0].Name)
	require.Equal(t, []string{"log", "--oneline", "-1", "--", docPath}, ir.Invocations[0].Args)
}

func TestParseWithBridgeCommandInvocationsAfterSetLocation(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	docPath := "docs/superpowers/plans/2026-07-02-unified-directory-approval.md"
	ir, err := parseWithBridge("Set-Location C:\\repo; git log --oneline -1 -- " + docPath)
	require.NoError(t, err)
	require.NotNil(t, ir)
	require.Len(t, ir.Invocations, 2)
	require.Equal(t, "set-location", ir.Invocations[0].Name)
	require.Equal(t, []string{"C:\\repo"}, ir.Invocations[0].Args)
	require.Equal(t, "git", ir.Invocations[1].Name)
	require.Equal(t, []string{"log", "--oneline", "-1", "--", docPath}, ir.Invocations[1].Args)
	require.Contains(t, ir.Paths, "C:\\repo")
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

// TestGetWindowsShellPathResolvesHost locks in that the classification
// bridge resolves the same PowerShell host that executes shell_run /
// powershell_run commands (pwsh.exe if present, otherwise powershell.exe).
// The bridge MUST match the executor so AST parsing reflects the grammar
// that will actually run. GitHub Actions Windows runners ship pwsh.exe on
// PATH, so this assertion proves the resolver picks pwsh.exe when available.
func TestGetWindowsShellPathResolvesHost(t *testing.T) {
	p := getWindowsShellPath()
	if p == "" {
		t.Skip("no PowerShell host on PATH")
	}
	base := filepath.Base(p)
	if base != "pwsh.exe" && base != "powershell.exe" {
		t.Fatalf("expected pwsh.exe or powershell.exe, got %q", base)
	}
	// When pwsh.exe is on PATH, it must be preferred.
	if pwshPath, err := exec.LookPath("pwsh.exe"); err == nil {
		require.Equal(t, filepath.Base(pwshPath), base,
			"classifier bridge must prefer pwsh.exe when present to match the executor")
	}
}

// TestIsReadOnlyPowerShellPipelineChainSemantics guards the classifier's
// handling of PS7-only pipeline-chain operators (&&, ||) and null-coalescing
// (??). Under pwsh.exe these parse as PipelineChainAst; the bridge extracts
// the operator and each chained command, and readonly_ps.go checks that every
// command is in the read-only allowlist — so a chain of read-only commands
// is read-only, while a chain containing a writer (Remove-Item) is not. Under
// powershell.exe (PS5.1) these are parse errors, so the classifier fail-closes
// (not read-only). Both hosts converge on the same safe outcome for mixed
// chains: a writer in the chain triggers review.
func TestIsReadOnlyPowerShellPipelineChainSemantics(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	pwshAvailable := func() bool {
		_, err := exec.LookPath("pwsh.exe")
		return err == nil
	}

	if pwshAvailable() {
		// Under pwsh.exe: read-only chain is read-only.
		got, _, _ := IsReadOnlyPowerShell("Get-Content a && Get-Content b")
		require.Truef(t, got, "read-only chain under pwsh.exe must be read-only")
		// Under pwsh.exe: chain with a writer is NOT read-only.
		got, _, _ = IsReadOnlyPowerShell("Get-Content a && Remove-Item b")
		require.Falsef(t, got, "chain with Remove-Item under pwsh.exe must NOT be read-only")
		return
	}

	// Under powershell.exe (PS5.1) only: PS7-only operators are parse errors,
	// so the classifier fail-closes (not read-only), matching the PS5.1
	// executor which cannot run them either.
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
