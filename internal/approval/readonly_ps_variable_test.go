package approval

import "testing"

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
