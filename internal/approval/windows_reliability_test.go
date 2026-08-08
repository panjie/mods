//go:build windows && windowsreliability

package approval

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWindowsReliabilityPowerShellHostAndGrammar(t *testing.T) {
	t.Cleanup(CloseBridge)
	host := getWindowsShellPath()
	if host == "" {
		t.Fatal("blocked/not covered: no PowerShell host on PATH")
	}
	expected := strings.ToLower(strings.TrimSpace(os.Getenv("MODS_EXPECT_POWERSHELL_HOST")))
	if expected != "" && !strings.EqualFold(filepath.Base(host), expected) {
		t.Fatalf("selected host=%q, expected %q; the runner PATH did not isolate the requested PowerShell", host, expected)
	}
	if pwsh, err := exec.LookPath("pwsh.exe"); err == nil && !strings.EqualFold(host, pwsh) {
		t.Fatalf("pwsh is available at %q but resolver selected %q", pwsh, host)
	}

	ir, err := parseWithBridge(`"profile=$PROFILE"; "unicode=项目"; Write-Error "错误"`)
	if err != nil {
		t.Fatalf("parse profile/unicode/error command with %q: %v", host, err)
	}
	if ir.Version != "1" || len(ir.Commands) == 0 {
		t.Fatalf("incomplete bridge IR: %#v", ir)
	}

	_, err = parseWithBridge(`Get-Content a && Get-Content b`)
	isPS7 := strings.EqualFold(filepath.Base(host), "pwsh.exe")
	if isPS7 && err != nil {
		t.Fatalf("PowerShell 7 pipeline chain rejected: %v", err)
	}
	readOnly, _, _ := IsReadOnlyPowerShell(`Get-Content a && Remove-Item b`)
	if readOnly {
		t.Fatal("pipeline chain containing Remove-Item classified read-only")
	}

	bad, err := parseWithBridge(`Get-Content "unterminated`)
	if err != nil {
		t.Fatalf("bridge transport failed for syntax error: %v", err)
	}
	if len(bad.ParseErrors) == 0 {
		t.Fatal("PowerShell syntax error was not reported in IR")
	}
	t.Logf("verified PowerShell host=%q version=%q profile/Unicode/error and grammar behavior", host, ir.Version)
}
