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

		// Bounded safe script-block pipelines
		{"foreach local line count", `Get-ChildItem -Recurse -Filter *.go | Select-Object FullName | ForEach-Object { $lines = (Get-Content $_.FullName | Measure-Object -Line).Lines; "$($_.FullName): $lines lines" } | Sort-Object { [int]($_.Split(':')[1].Trim().Split(' ')[0]) } -Descending`},
		{"foreach read content", `Get-ChildItem -Filter *.go | ForEach-Object { Get-Content $_.FullName | Measure-Object -Line }`},
		{"where script block predicate", `Get-ChildItem | Where-Object { $_.Name -like '*.go' }`},
		{"sort script block expression", `Get-ChildItem | Sort-Object { $_.Name.Trim().Split('.')[0] }`},
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
		// Unsafe script blocks
		{"script block writer", `Get-ChildItem | ForEach-Object { Remove-Item $_.FullName }`},
		{"script block env assignment", `Get-ChildItem | ForEach-Object { $env:MODS_TEST = $_.FullName }`},
		{"script block unassigned variable path", `Get-ChildItem | ForEach-Object { Get-Content $path }`},
		{"script block assigned variable path", `Get-ChildItem | ForEach-Object { $p = 'C:\Users\Public\secret.txt'; Get-Content $p }`},
		{"script block braced assigned variable path", `Get-ChildItem | ForEach-Object { $p = 'C:\Users\Public\secret.txt'; Get-Content ${p} }`},
		{"script block splatted assigned variable path", `Get-ChildItem | ForEach-Object { $p = @{Path='C:\Users\Public\secret.txt'}; Get-Content @p }`},
		{"top-level assignment with script block", `$x = Get-Date; Get-ChildItem | Where-Object { $_.Name -like '*.go' }`},
		{"parenthesized top-level assignment with script block", `($x = 'v'); Get-ChildItem | Where-Object { $_.Name -like '*.go' }`},
		{"script block unsafe instance method", `Get-ChildItem | ForEach-Object { $_.Delete() }`},
		{"foreach without script block", `Get-Item .\owned.txt | ForEach-Object`},
		{"foreach member-name delete", `Get-Item .\owned.txt | ForEach-Object -MemberName Delete`},
		{"foreach positional member-name delete", `Get-Item .\owned.txt | ForEach-Object Delete`},
		{"static method mutation", `[IO.File]::Delete('owned.txt')`},
		{"static property access", `[Environment]::UserName`},
		{"join path home", `Get-ChildItem | ForEach-Object { Get-Content (Join-Path $HOME '.ssh') }`},
		{"command subexpression path", `Get-Content $(Get-ChildItem)`},

		// Subexpressions
		{"subexpression", "$(Get-Date)"},
		{"subexpression in arg", "Get-Content $(Get-ChildItem)"},
		{"static member delete in expandable string", `Write-Output "$([IO.File]::Delete('owned.txt'))"`},

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
		{"git diff output file", "git diff --output=owned.txt"},
		{"git show output file", "git show --output owned.txt HEAD"},
		{"git diff external helper", "git diff --ext-diff"},

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
