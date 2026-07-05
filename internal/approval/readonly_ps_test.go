//go:build windows

package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadOnlyPowerShellReadOnly(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cases := []struct {
		name string
		cmd  string
	}{
		// Simple cmdlets
		{"get-childitem", "Get-ChildItem"},
		{"get-content", "Get-Content file.txt"},
		{"test-path", "Test-Path x"},
		{"get-process", "Get-Process"},
		{"get-date", "Get-Date"},
		{"get-location", "Get-Location"},

		// With parameters
		{"get-childitem params", "Get-ChildItem -Path C:\\Users -Recurse"},
		{"get-content params", "Get-Content -Path file.txt -TotalCount 10"},

		// Pipelines (all read-only)
		{"pipe sort", "Get-ChildItem | Sort-Object Name"},
		{"pipe select", "Get-Process | Select-Object Name,Id"},
		{"pipe measure", "Get-ChildItem | Measure-Object"},
		{"pipe where simplified", "Get-ChildItem | Where-Object Name -eq 'foo'"},

		// Aliases
		{"alias gci", "gci"},
		{"alias ls", "ls"},
		{"alias cat", "cat file.txt"},
		{"alias gps select", "gps | select Name"},

		// Multiple read-only commands with ;
		{"semicolon", "Get-Date; Get-Process"},
		{"set-location then git log", "Set-Location C:\\repo; git log --oneline -1 -- docs/superpowers/plans/2026-07-02-unified-directory-approval.md"},
		{"cd then git status", "cd C:\\repo; git status"},

		// Out-Null (discard)
		{"out-null", "Get-Process | Out-Null"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, _ := IsReadOnlyPowerShell(c.cmd)
			require.Truef(t, got, "cmd=%q should be read-only", c.cmd)
		})
	}
}

func TestIsReadOnlyPowerShellNotReadOnly(t *testing.T) {
	t.Cleanup(func() { CloseBridge() })

	cases := []struct {
		name string
		cmd  string
	}{
		// Script blocks
		{"script block where", "Where-Object { $_.Name -eq 'foo' }"},
		{"script block foreach", "ForEach-Object { $_ }"},

		// Subexpressions
		{"subexpression", "$(Get-Date)"},
		{"subexpression in arg", "Get-Content $(Get-ChildItem)"},

		// Variable args
		{"variable arg", "Get-Content $env:SECRET"},
		{"variable arg 2", "Get-Process $var"},

		// Redirections
		{"redirect out", "Get-Process > out.txt"},
		{"redirect append", "Get-Date >> log.txt"},

		// Assignment
		{"assignment", "$x = Get-Date"},

		// Control flow
		{"if", "if ($true) { Get-Date }"},
		{"foreach", "foreach ($f in $files) { Get-Content $f }"},

		// Background
		{"background", "Get-Process &"},

		// Write cmdlets
		{"set-content", "Set-Content file.txt 'hello'"},
		{"remove-item", "Remove-Item file.txt"},
		{"new-item", "New-Item -Path x"},
		{"set-location then git push", "Set-Location C:\\repo; git push"},
		{"set-location then remove-item", "Set-Location C:\\repo; Remove-Item x"},

		// Excluded cmdlets (security traps)
		{"get-command", "Get-Command"},
		{"get-help", "Get-Help"},
		{"select-xml", "Select-Xml"},

		// Stop parsing
		{"stop parsing", "git --% --foo"},

		// Empty
		{"empty", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _, _ := IsReadOnlyPowerShell(c.cmd)
			require.Falsef(t, got, "cmd=%q should NOT be read-only", c.cmd)
		})
	}
}
