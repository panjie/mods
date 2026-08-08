package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellHomeVariableUsesAreExplicitPaths(t *testing.T) {
	tests := []struct {
		name     string
		command  string
		variable string
		want     bool
	}{
		{"user profile path", `Get-Content $env:USERPROFILE\.config\mods\mods.yml`, "env:userprofile", true},
		{"braced user profile path", `Get-Content ${env:USERPROFILE}\.config\mods\mods.yml`, "env:userprofile", true},
		{"home path", `Get-Content $HOME\.config\mods\mods.yml`, "home", true},
		{"braced home path", `Get-Content ${HOME}/.config/mods/mods.yml`, "home", true},
		{"bare home", `Get-Content $HOME`, "home", false},
		{"join path home", `Get-Content (Join-Path $HOME '.ssh')`, "home", false},
		{"mixed uses", `Get-Content $HOME\.ssh\config; Write-Output $HOME`, "home", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := powerShellHomeVariableUsesAreExplicitPaths(tt.command, tt.variable); got != tt.want {
				t.Fatalf("powerShellHomeVariableUsesAreExplicitPaths(%q, %q) = %v, want %v", tt.command, tt.variable, got, tt.want)
			}
		})
	}
}

func TestPowerShellSubshellsOnlyInTopLevelValues(t *testing.T) {
	tests := []struct {
		name        string
		command     string
		expressions []string
		want        bool
	}{
		{
			name:        "subshell only as top-level value",
			command:     `"$(Get-Date)"`,
			expressions: []string{`"$(Get-Date)"`},
			want:        true,
		},
		{
			name:        "subshell in non-top-level position",
			command:     `Write-Host "$(Remove-Item x)"`,
			expressions: []string{`"$(Get-Date)"`},
			want:        false,
		},
		{
			name:        "no subshell at all",
			command:     `Get-Date`,
			expressions: nil,
			want:        true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, powerShellSubshellsOnlyInTopLevelValues(tc.command, tc.expressions))
		})
	}
}
