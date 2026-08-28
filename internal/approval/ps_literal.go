package approval

import (
	"strings"
)

// powerShellLiteralAssignments extracts, for each PowerShell variable assigned
// exactly once at top level with a pure string-literal value, the literal
// string reported by the PowerShell AST bridge. It is fail-closed: variables
// assigned multiple times, assigned inside a script block, or lacking an
// AST-proven ordinary string constant are omitted.
func powerShellLiteralAssignments(ir *psBridgeIR) map[string]string {
	if ir == nil || len(ir.AssignmentTargets) == 0 || len(ir.LiteralAssignments) == 0 {
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

	result := map[string]string{}
	for candidate, value := range ir.LiteralAssignments {
		name := barePowerShellVarName(candidate)
		count := topLevel[name]
		if count != 1 || scriptBlock[name] || !simplePowerShellLocalVar.MatchString(name) {
			continue
		}
		result[name] = value
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
func powerShellLiteralAssigned(ir *psBridgeIR) map[string]bool {
	literals := powerShellLiteralAssignments(ir)
	if len(literals) == 0 {
		return nil
	}
	set := make(map[string]bool, len(literals))
	for name := range literals {
		set[name] = true
	}
	return set
}
