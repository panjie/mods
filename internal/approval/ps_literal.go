package approval

import (
	"regexp"
	"strings"
)

// PowerShell here-strings (@'...'@ and @"..."@) may embed arbitrary text that
// is not part of the parsed command. Before scanning command text for literal
// assignments, their bodies are blanked (newlines preserved) so a line inside
// a here-string cannot masquerade as an assignment statement.
var (
	rePowerShellHereStringSingle = regexp.MustCompile(`(?s)@'(.*?)\r?\n'@`)
	rePowerShellHereStringDouble = regexp.MustCompile(`(?s)@"(.*?)\r?\n"@`)
)

func blankPowerShellHereStrings(command string) string {
	command = rePowerShellHereStringSingle.ReplaceAllStringFunc(command, blankPreserveNewlines)
	command = rePowerShellHereStringDouble.ReplaceAllStringFunc(command, blankPreserveNewlines)
	return command
}

func blankPreserveNewlines(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\n' && b[i] != '\r' {
			b[i] = ' '
		}
	}
	return string(b)
}

// powerShellLiteralAssignments extracts, for each PowerShell variable assigned
// exactly once at top level with a pure string-literal value, the literal
// string. It is fail-closed: variables assigned multiple times, assigned inside
// a script block, assigned a non-literal or interpolated value, or whose
// assignment text is ambiguous are omitted.
func powerShellLiteralAssignments(command string, ir *psBridgeIR) map[string]string {
	if ir == nil || len(ir.AssignmentTargets) == 0 {
		return nil
	}
	topLevel := map[string]int{}
	for _, target := range ir.AssignmentTargets {
		topLevel[barePowerShellVarName(target)]++
	}
	scriptBlock := map[string]bool{}
	for _, target := range ir.ScriptBlockAssignmentTargets {
		scriptBlock[barePowerShellVarName(target)] = true
	}

	blanked := blankPowerShellHereStrings(command)
	result := map[string]string{}
	for name, count := range topLevel {
		if count != 1 || scriptBlock[name] || !simplePowerShellLocalVar.MatchString(name) {
			continue
		}
		re := literalAssignRegex(name)
		matches := re.FindAllStringSubmatch(blanked, -1)
		if len(matches) != 1 {
			continue
		}
		value := matches[0][1]
		if len(value) < 2 {
			continue
		}
		inner := value[1 : len(value)-1]
		if strings.ContainsAny(inner, `'"`) {
			// An embedded quote means an escaped literal ('' or `") rather than
			// a plain path value.
			continue
		}
		result[name] = inner
	}
	return result
}

// barePowerShellVarName reduces an assignment target such as "$p" or "${p}" to
// its lowercase bare name.
func barePowerShellVarName(target string) string {
	name := strings.TrimSpace(target)
	name = strings.TrimPrefix(name, "$")
	name = strings.Trim(name, "{}")
	return strings.ToLower(name)
}

// powerShellLiteralAssigned returns the set of variables assigned exactly once
// at top level with a pure string-literal value. Such assignments are inert, so
// the static read-only proof may treat their targets as known-safe values.
func powerShellLiteralAssigned(command string, ir *psBridgeIR) map[string]bool {
	literals := powerShellLiteralAssignments(command, ir)
	if len(literals) == 0 {
		return nil
	}
	set := make(map[string]bool, len(literals))
	for name := range literals {
		set[name] = true
	}
	return set
}

// literalAssignRegex matches a top-level assignment of name to a simple
// string literal: either a double-quoted string with no interpolation ($ or
// backtick) or a single-quoted string with no embedded quotes. The statement
// boundary anchor (start of text, newline, or semicolon) rejects occurrences
// inside other string literals on the same line.
func literalAssignRegex(name string) *regexp.Regexp {
	pattern := `(?i)(?:^|[\n;])\s*\$\{?` + regexp.QuoteMeta(name) + `\}?\s*=\s*("[^"\x60$]*"|'(?:[^']|'')*')`
	return regexp.MustCompile(pattern)
}
