package approval

import (
	"runtime"
	"strings"
)

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

func commandHasPowerShellCompoundSyntax(s string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				if inSingle && i+1 < len(s) && s[i+1] == '\'' {
					i++
					continue
				}
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';', '|', '&', '{', '}', '\r', '\n':
			if !inSingle && !inDouble {
				return true
			}
		}
	}
	return false
}

func normalizeSimpleCommand(command string) string {
	return strings.TrimSpace(command)
}

func hasShellRedirection(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '>' {
			return true
		}
	}
	return false
}

func isRedirectionToken(token string) bool {
	return token == ">" || token == ">>" || token == "1>" || token == "1>>" || token == "2>" || token == "2>>"
}

// exactShellCommands are commands that should always produce an exact
// (full-string) approval rule rather than a prefix rule, because their
// arguments frequently change behavior in unsafe ways.
var exactShellCommands = map[string]bool{
	"awk": true, "bash": true, "dash": true, "doas": true, "env": true,
	"eval": true, "exec": true, "find": true, "flock": true, "ionice": true,
	"ksh": true, "node": true, "perl": true, "python": true, "python3": true,
	"ruby": true, "sed": true, "setsid": true, "sh": true, "sudo": true,
	"tee": true, "watch": true, "xargs": true, "zsh": true,
}

// subcommandShellCommands have a well-known subcommand structure
// (e.g. `git push`); prefix rules should include the subcommand.
var subcommandShellCommands = map[string]bool{
	"bun": true, "cargo": true, "docker": true, "gh": true, "git": true,
	"go": true, "helm": true, "kubectl": true, "npm": true, "pnpm": true,
	"yarn": true,
}

// flagPrefixShellCommands accept flags before the path operand; prefix
// rules should skip the flags.
var flagPrefixShellCommands = map[string]bool{
	"chmod": true, "chown": true, "cp": true, "mkdir": true, "mv": true,
	"rm": true, "rmdir": true, "touch": true,
}

// shellToolUsesPOSIX reports whether the named shell tool should be
// parsed with the POSIX grammar. PowerShell (Windows) uses the simple
// tokenizer instead.
func shellToolUsesPOSIX(tool string) bool {
	if tool == "powershell_run" {
		return false
	}
	return runtime.GOOS != "windows"
}

func shellPrefixLength(args []string) int {
	if len(args) < 2 {
		return len(args)
	}
	command := args[0]
	if subcommandShellCommands[command] {
		if strings.HasPrefix(args[1], "-") {
			return 0
		}
		index := 2
		if (command == "npm" || command == "pnpm" || command == "yarn" || command == "bun") &&
			index < len(args) && args[index-1] == "run" {
			index++
		}
		return index
	}
	if flagPrefixShellCommands[command] {
		index := 1
		for index < len(args) && strings.HasPrefix(args[index], "-") {
			index++
		}
		return index
	}
	return 1
}
