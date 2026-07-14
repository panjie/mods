package approval

import (
	"regexp"
	"strings"
)

// IsReadOnlyPowerShell analyzes a PowerShell command using a persistent
// PowerShell bridge process (pwsh.exe if present, otherwise powershell.exe)
// that calls System.Management.Automation.Language.Parser.
// Returns (true, reason, paths) when read-only; (false, "", nil) when not or
// inconclusive (fail-closed — caller degrades to LLM classifier). The paths
// return value contains AST-extracted string literal argument values that the
// caller can filter to external paths for AffectedDirs.
func IsReadOnlyPowerShell(command string) (bool, string, []string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, "", nil
	}

	ir, err := parseWithBridge(command)
	if err != nil {
		return false, "", nil
	}

	// Parse errors → fail-closed
	if len(ir.ParseErrors) > 0 {
		return false, "", nil
	}

	// Security flags — any hit means not read-only
	if ir.HasControlFlow {
		return false, "", nil
	}
	if ir.HasBackground {
		return false, "", nil
	}
	if ir.HasStopParsing {
		return false, "", nil
	}

	// File redirection → writes to filesystem
	for _, r := range ir.Redirects {
		if r == "FileRedirection" {
			return false, "", nil
		}
	}

	if hasPowerShellExpansion(ir, "subshell") {
		return false, "", nil
	}
	if !safePowerShellAssignments(ir) {
		return false, "", nil
	}
	if !safePowerShellVariables(ir) {
		return false, "", nil
	}
	if !safePowerShellMethods(ir) {
		return false, "", nil
	}
	if len(ir.ForEachMemberNames) > 0 {
		return false, "", nil
	}
	if powerShellCommandArgsContainUnsafeVariable(ir) {
		return false, "", nil
	}

	// Encoded command → hides intent
	for _, rf := range ir.RiskFlags {
		if rf == "invoke_expression" {
			return false, "", nil
		}
	}

	// No commands → fail-closed (empty or expression-only)
	if len(ir.Commands) == 0 {
		return false, "", nil
	}

	// All command invocations must be in the read-only allowlist. Prefer the
	// invocation list because it preserves argv, which lets us recognize
	// read-only external subcommands such as `git log`.
	invocations := ir.Invocations
	if len(invocations) == 0 {
		for _, cmd := range ir.Commands {
			invocations = append(invocations, psCommandInvocation{Name: cmd})
		}
	}
	for _, inv := range invocations {
		if !readOnlyPowerShellInvocation(inv) {
			return false, "", nil
		}
	}

	return true, "read-only PowerShell command (AST analysis)", ir.Paths
}

func readOnlyPowerShellInvocation(inv psCommandInvocation) bool {
	name := normalizePowerShellCommandName(inv.Name)
	if name == "foreach-object" && !powerShellInvocationHasScriptBlockArg(inv) {
		return false
	}
	if readOnlyPowerShellCmdlets[name] {
		return true
	}
	return readOnlySubcommandInvocation(name, inv.Args)
}

func powerShellInvocationHasScriptBlockArg(inv psCommandInvocation) bool {
	for _, arg := range inv.Args {
		if strings.HasPrefix(strings.TrimSpace(arg), "{") {
			return true
		}
	}
	return false
}

func normalizePowerShellCommandName(name string) string {
	name = strings.ToLower(trimPowerShellLiteral(name))
	name = strings.ReplaceAll(name, "/", `\`)
	if i := strings.LastIndex(name, `\`); i >= 0 {
		name = name[i+1:]
	}
	for _, suffix := range []string{".exe", ".cmd", ".bat"} {
		name = strings.TrimSuffix(name, suffix)
	}
	return name
}

func trimPowerShellLiteral(s string) string {
	return strings.Trim(strings.TrimSpace(s), `"'`)
}

var simplePowerShellLocalVar = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var safePipelinePowerShellVariables = map[string]bool{
	"_": true, "psitem": true, "true": true, "false": true, "null": true,
	"args": true, "input": true,
}

var purePowerShellMethodNames = map[string]bool{
	"trim":     true,
	"split":    true,
	"tostring": true,
}

func normalizePowerShellVariableName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "$")
	return strings.ToLower(name)
}

func assignedPowerShellLocals(ir *psBridgeIR) map[string]bool {
	assigned := map[string]bool{}
	for _, target := range ir.ScriptBlockAssignmentTargets {
		name := normalizePowerShellVariableName(target)
		if simplePowerShellLocalVar.MatchString(name) {
			assigned[name] = true
		}
	}
	return assigned
}

func safePowerShellAssignments(ir *psBridgeIR) bool {
	if !ir.HasAssignment {
		return true
	}
	if !ir.HasScriptBlock || len(ir.AssignmentTargets) == 0 || len(ir.ScriptBlockAssignmentTargets) == 0 {
		return false
	}
	scriptBlockAssignments := map[string]int{}
	for _, target := range ir.ScriptBlockAssignmentTargets {
		name := normalizePowerShellVariableName(target)
		scriptBlockAssignments[name]++
	}
	for _, target := range ir.AssignmentTargets {
		name := normalizePowerShellVariableName(target)
		if strings.Contains(name, ":") || !simplePowerShellLocalVar.MatchString(name) {
			return false
		}
		if scriptBlockAssignments[name] == 0 {
			return false
		}
		scriptBlockAssignments[name]--
	}
	return true
}

func safePowerShellVariables(ir *psBridgeIR) bool {
	assigned := assignedPowerShellLocals(ir)
	for _, variable := range ir.Variables {
		name := normalizePowerShellVariableName(variable)
		if safePipelinePowerShellVariables[name] || assigned[name] {
			continue
		}
		return false
	}
	return true
}

func safePowerShellMethods(ir *psBridgeIR) bool {
	if len(ir.StaticMembers) > 0 {
		return false
	}
	for _, method := range ir.MethodInvocations {
		if !purePowerShellMethodNames[strings.ToLower(strings.TrimSpace(method))] {
			return false
		}
	}
	return true
}

func hasPowerShellExpansion(ir *psBridgeIR, expansion string) bool {
	for _, e := range ir.Expansions {
		if e == expansion {
			return true
		}
	}
	return false
}

func powerShellCommandArgsContainUnsafeVariable(ir *psBridgeIR) bool {
	assigned := assignedPowerShellLocals(ir)
	if len(assigned) == 0 {
		return false
	}
	for _, inv := range ir.Invocations {
		name := normalizePowerShellCommandName(inv.Name)
		for _, arg := range inv.Args {
			trimmed := strings.TrimSpace(arg)
			if (name == "foreach-object" || name == "where-object" || name == "sort-object") && strings.HasPrefix(trimmed, "{") {
				continue
			}
			lower := strings.ToLower(trimmed)
			for local := range assigned {
				if strings.Contains(lower, "$"+local) || strings.Contains(lower, "${"+local+"}") || lower == "@"+local {
					return true
				}
			}
		}
	}
	return false
}

// readOnlyPowerShellCmdlets is the allowlist of PowerShell cmdlets and
// aliases that are always read-only. All names are lowercase.
var readOnlyPowerShellCmdlets = map[string]bool{
	// Filesystem reads
	"get-childitem": true, "gci": true, "ls": true, "dir": true,
	"get-content": true, "gc": true, "cat": true, "type": true,
	"get-item": true, "gi": true,
	"get-itemproperty":      true,
	"get-itempropertyvalue": true,
	"test-path":             true,
	"resolve-path":          true,
	"get-filehash":          true,
	"get-acl":               true,
	"select-string":         true,
	"get-location":          true, "gl": true, "pwd": true,
	"get-psdrive":    true,
	"get-psprovider": true,
	"convert-path":   true,
	"join-path":      true,
	"split-path":     true,

	// Current-process location changes. These alter only the transient
	// PowerShell working context used by this tool call.
	"set-location": true, "cd": true, "chdir": true, "sl": true,
	"push-location": true, "pushd": true,
	"pop-location": true, "popd": true,

	// Object inspection / transforms (pure)
	"get-member": true, "gm": true,
	"get-unique": true, "gu": true,
	"compare-object": true, "compare": true,
	"join-string":      true,
	"get-random":       true,
	"convertto-json":   true,
	"convertfrom-json": true,
	"convertto-csv":    true,
	"convertfrom-csv":  true,
	"convertto-xml":    true,
	"convertto-html":   true,
	"format-hex":       true,

	// Pipeline transformers
	"select-object": true, "select": true,
	"foreach-object": true,
	"sort-object":    true, "sort": true,
	"group-object": true, "group": true,
	"where-object": true, "?": true, "where": true,
	"measure-object": true, "measure": true,
	"format-table": true, "ft": true,
	"format-list": true, "fl": true,
	"format-wide": true, "fw": true,
	"format-custom": true, "fc": true,
	"out-string": true,
	"out-host":   true,
	"out-null":   true,

	// Output
	"write-output": true, "write": true, "echo": true,
	"write-host": true,

	// System info
	"get-process": true, "gps": true, "ps": true,
	"get-service": true, "gsv": true,
	"get-computerinfo": true,
	"get-host":         true,
	"get-date":         true, "date": true,
	"get-hotfix":    true,
	"get-timezone":  true,
	"get-uptime":    true,
	"get-culture":   true,
	"get-uiculture": true,
	"get-alias":     true, "gal": true,
	"get-history": true, "h": true, "history": true,

	// Other
	"start-sleep": true, "sleep": true,
}
