package approval

import (
	"path"
	"strings"
)

// ReadOnlyCommandPolicy contains command names the user explicitly trusts as
// read-only. These entries supplement the built-in command tables and take
// precedence over command-specific argument checks. Shell syntax and compound
// command safety checks still run before a leaf invocation reaches the policy.
type ReadOnlyCommandPolicy struct {
	Commands []string
}

func (p ReadOnlyCommandPolicy) matchesPOSIX(name string) bool {
	name = path.Base(name)
	for _, command := range p.Commands {
		if name == command {
			return true
		}
	}
	return false
}

func (p ReadOnlyCommandPolicy) matchesPowerShell(name string) bool {
	name = normalizePowerShellCommandName(name)
	for _, command := range p.Commands {
		if name == normalizePowerShellCommandName(command) {
			return true
		}
	}
	return false
}

func (p ReadOnlyCommandPolicy) reason(name string) string {
	return "user-configured read-only command: " + strings.TrimSpace(name)
}
