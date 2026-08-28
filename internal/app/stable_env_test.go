package app

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCommandMutatesPowerShellEnvironment(t *testing.T) {
	mutations := []string{
		`$env:TEMP = "C:\elsewhere"`,
		`$env:TEMP += "suffix"`,
		`${env:TEMP} = "C:\elsewhere"`,
		`Set-Item -Path Env:TEMP -Value "C:\elsewhere"`,
		`Remove-Item Env:\TEMP`,
		`New-Item -Path Env:TRACKER -Value 1`,
		`Set-Content -Path Env:\TEMP -Value 'x'`,
		`[Environment]::SetEnvironmentVariable('TEMP', 'C:\elsewhere', 'Process')`,
		`[System.Environment]::SetEnvironmentVariable('TEMP', 'C:\elsewhere', 'Process')`,
		`[ system.environment ] :: SetEnvironmentVariable('TEMP', 'C:\elsewhere', 'Process')`,
	}
	for _, command := range mutations {
		require.True(t, commandMutatesPowerShellEnvironment(command), command)
	}

	reads := []string{
		`Get-Content $env:TEMP\notes.txt`,
		`Test-Path $env:SystemRoot\WinSxS`,
		`Write-Output ($env:TEMP -eq 'x')`,
		`Set-Content -Path "$env:TEMP\profile_init.el" -Value "x"`,
		`[System.Environment]::GetEnvironmentVariable('TEMP')`,
		`[NotEnvironment]::SetEnvironmentVariable('TEMP', 'x')`,
	}
	for _, command := range reads {
		require.False(t, commandMutatesPowerShellEnvironment(command), command)
	}
	require.True(t, commandMutatesPowerShellEnvironment(`Write-Output "$env:USERNAME=$env:TEMP"`),
		"an equals sign right after an env name is treated as a mutation (conservative)")
}
