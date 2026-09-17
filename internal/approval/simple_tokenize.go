package approval

import "strings"

// Hand-written tokenizer used as a fallback when the POSIX shell parser
// cannot be applied (Windows / PowerShell). It is intentionally simple
// and operates purely on strings; it does not depend on mvdan.cc/sh.

func tokenizeSimple(s string) []string {
	var tokens []string
	var current strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' || ch == '\'' {
			if quote == '\'' && ch == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				current.WriteByte('\'')
				i++
				continue
			}
			if quote == ch {
				tokens = append(tokens, current.String())
				current.Reset()
				quote = 0
			} else if quote == 0 {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				quote = ch
			} else {
				current.WriteByte(ch)
			}
			continue
		}
		if quote == 0 && (ch == ' ' || ch == '\t') {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func splitSimpleCompound(s string) []string {
	var parts []string
	start := 0
	var quote byte
	for i := 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\'' {
			if quote == '\'' && s[i] == '\'' && i+1 < len(s) && s[i+1] == '\'' {
				i++
				continue
			}
			if quote == s[i] {
				quote = 0
			} else if quote == 0 {
				quote = s[i]
			}
			continue
		}
		if quote != 0 {
			continue
		}
		if i+1 < len(s) && s[i] == '&' && s[i+1] == '&' {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 2
			i++
			continue
		}
		if i+1 < len(s) && s[i] == '|' && s[i+1] == '|' {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 2
			i++
			continue
		}
		if s[i] == '&' && (i == 0 || s[i-1] != '>') {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(s[start:]))
	}
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func normalizeSimpleCommand(command string) string {
	return strings.TrimSpace(command)
}

func isRedirectionToken(token string) bool {
	return token == ">" || token == ">>" || token == "1>" || token == "1>>" || token == "2>" || token == "2>>"
}

// Command families used by the structural reviewability heuristic to identify
// operands that may themselves invoke a command. They do not grant permissions.
var exactShellCommands = map[string]bool{
	"awk": true, "bash": true, "dash": true, "doas": true, "env": true,
	"eval": true, "exec": true, "find": true, "flock": true, "ionice": true,
	"ksh": true, "node": true, "perl": true, "python": true, "python3": true,
	"ruby": true, "sed": true, "setsid": true, "sh": true, "sudo": true,
	"tee": true, "watch": true, "xargs": true, "zsh": true,
}

// subcommandShellCommands have a well-known subcommand structure.
var subcommandShellCommands = map[string]bool{
	"bun": true, "cargo": true, "docker": true, "gh": true, "git": true,
	"go": true, "helm": true, "kubectl": true, "npm": true, "pnpm": true,
	"yarn": true,
}

// flagPrefixShellCommands accept flags before the path operand.
var flagPrefixShellCommands = map[string]bool{
	"chmod": true, "chown": true, "cp": true, "mkdir": true, "mv": true,
	"rm": true, "rmdir": true, "touch": true,
}
