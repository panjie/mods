package commandanalysis

import "strings"

// Only inline programs with a bounded, conservative lexical proof are reads.
// No AWK executable, script file, extension, or external parser is invoked.
func readAWK(_ *commandEntry, args []string, _ ReadOnlyCommandPolicy, _ *catalogEvaluation) bool {
	program, _, ok := bindAWK(args)
	return ok && awkProgramReadOnly(program)
}

func awkReadOperands(args []string) ([]string, bool) {
	_, inputs, ok := bindAWK(args)
	return inputs, ok
}

func bindAWK(args []string) (program string, inputs []string, ok bool) {
	seen, options := false, true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if !seen && options && strings.HasPrefix(arg, "-") {
			switch {
			case arg == "-F" || arg == "-v":
				if i+1 == len(args) {
					return "", nil, false
				}
				i++
				if arg == "-v" && !awkAssignment(args[i]) {
					return "", nil, false
				}
			case strings.HasPrefix(arg, "-F") && len(arg) > 2:
			case strings.HasPrefix(arg, "-v") && len(arg) > 2:
				if !awkAssignment(arg[2:]) {
					return "", nil, false
				}
			default:
				return "", nil, false
			}
			continue
		}
		if !seen {
			program = arg
			seen = true
			continue
		}
		// Some implementations permute options after the program. Do not allow
		// profile/debug/load/file options to masquerade as input operands.
		if strings.HasPrefix(arg, "-") && arg != "-" {
			return "", nil, false
		}
		if name, _, assignment := strings.Cut(arg, "="); assignment && awkIdentifier(name) {
			if !awkAssignment(arg) {
				return "", nil, false
			}
			continue
		}
		if arg != "-" {
			inputs = append(inputs, arg)
		}
	}
	return program, inputs, seen
}

func awkIdentifier(s string) bool {
	if s == "" || !awkIdentStart(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !awkIdentStart(s[i]) && (s[i] < '0' || s[i] > '9') {
			return false
		}
	}
	return true
}
func awkIdentStart(c byte) bool { return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' }
func awkReserved(name string) bool {
	switch name {
	case "system", "getline", "function", "func", "ARGV", "ARGC", "ENVIRON", "PROCINFO", "SYMTAB", "FUNCTAB":
		return true
	}
	return false
}
func awkAssignment(arg string) bool {
	name, _, ok := strings.Cut(arg, "=")
	return ok && awkIdentifier(name) && !awkReserved(name)
}

// The scanner distinguishes strings/regex data from executable tokens. It
// rejects output redirection, command pipes,
// directives, continuation escapes and unrecognized characters. Slash is a
// regex delimiter only where an operand is required, never after an operand.
// Newlines retain that state to avoid hiding code in multiline expressions.
func awkProgramReadOnly(program string) bool {
	if strings.TrimSpace(program) == "" || len(program) > 64<<10 {
		return false
	}
	var tokens []string
	operand := true
	outputSeen := false
	for i := 0; i < len(program); {
		if len(tokens) >= 8192 {
			return false
		}
		c := program[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
			continue
		case c == '#':
			for i < len(program) && program[i] != '\n' {
				i++
			}
			continue
		case c == '"' || c == '/' && operand:
			delimiter := c
			i++
			closed := false
			for i < len(program) {
				c = program[i]
				i++
				if c == '\n' || c == '\r' {
					return false
				}
				if c == '\\' {
					if i == len(program) || program[i] == '\n' || program[i] == '\r' {
						return false
					}
					i++
					continue
				}
				if c == delimiter {
					closed = true
					break
				}
			}
			if !closed {
				return false
			}
			tokens = append(tokens, "literal")
			operand = false
		case awkIdentStart(c):
			start := i
			i++
			for i < len(program) && (awkIdentStart(program[i]) || program[i] >= '0' && program[i] <= '9') {
				i++
			}
			name := program[start:i]
			if name == "print" || name == "printf" {
				outputSeen = true
			}
			if awkReserved(name) {
				return false
			}
			tokens = append(tokens, name)
			switch name {
			case "print", "printf", "if", "else", "while", "do", "for", "exit", "in", "BEGIN", "END":
				operand = true
			default:
				operand = false
			}
		case c >= '0' && c <= '9':
			start := i
			i++
			for i < len(program) && (program[i] >= '0' && program[i] <= '9' || program[i] == '.') {
				i++
			}
			tokens = append(tokens, program[start:i])
			operand = false
		case c == '|':
			if i+1 >= len(program) || program[i+1] != '|' {
				return false
			}
			tokens = append(tokens, "||")
			i += 2
			operand = true
		case c == '>':
			// Before any print/printf, > can only be a comparison. Once
			// output appears, leave ambiguous comparison/redirection forms
			// unproven rather than trying to reconstruct statement scopes.
			if outputSeen {
				return false
			}
			tokens = append(tokens, ">")
			i++
			operand = true
		case c == '@' || c == '\\':
			return false
		case strings.ContainsRune("{}()[];,?:$+-*/%^=<!~&.", rune(c)):
			token := string(c)
			i++
			if i < len(program) && (c == '+' && program[i] == '+' || c == '-' && program[i] == '-') {
				token += string(program[i])
				i++
			} else {
				switch c {
				case ')', ']':
					operand = false
				default:
					operand = true
				}
			}
			tokens = append(tokens, token)
		default:
			return false
		}
	}
	for i, token := range tokens {
		if i+1 < len(tokens) && tokens[i+1] == "(" && awkIdentifier(token) {
			switch token {
			case "if", "while", "for", "print", "printf", "exit",
				"length", "substr", "index", "split", "match", "sub", "gsub", "sprintf",
				"tolower", "toupper", "int", "sqrt", "log", "exp", "sin", "cos", "atan2", "rand", "srand":
			default:
				return false
			}
		}
	}
	return len(tokens) > 0
}
