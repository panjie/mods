package approval

import (
	"strings"
)

// IsReadOnlyPowerShell analyzes a PowerShell command using a persistent
// pwsh.exe bridge process that calls System.Management.Automation.Language.Parser.
// Returns (true, reason) when read-only; (false, "") when not or inconclusive
// (fail-closed — caller degrades to LLM classifier).
func IsReadOnlyPowerShell(command string) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, ""
	}

	ir, err := parseWithBridge(command)
	if err != nil {
		return false, ""
	}

	// Parse errors → fail-closed
	if len(ir.ParseErrors) > 0 {
		return false, ""
	}

	// Security flags — any hit means not read-only
	if ir.HasScriptBlock {
		return false, ""
	}
	if ir.HasAssignment {
		return false, ""
	}
	if ir.HasControlFlow {
		return false, ""
	}
	if ir.HasBackground {
		return false, ""
	}
	if ir.HasStopParsing {
		return false, ""
	}

	// File redirection → writes to filesystem
	for _, r := range ir.Redirects {
		if r == "FileRedirection" {
			return false, ""
		}
	}

	// Subexpression $(...) → can execute arbitrary code
	for _, e := range ir.Expansions {
		if e == "subshell" {
			return false, ""
		}
	}

	// Variable args → may leak secrets via error messages
	for _, e := range ir.Expansions {
		if e == "var" {
			return false, ""
		}
	}

	// Encoded command → hides intent
	for _, rf := range ir.RiskFlags {
		if rf == "invoke_expression" {
			return false, ""
		}
	}

	// No commands → fail-closed (empty or expression-only)
	if len(ir.Commands) == 0 {
		return false, ""
	}

	// All commands must be in the read-only allowlist
	for _, cmd := range ir.Commands {
		if !readOnlyPowerShellCmdlets[cmd] {
			return false, ""
		}
	}

	return true, "read-only PowerShell command (AST analysis)"
}

// readOnlyPowerShellCmdlets is the allowlist of PowerShell cmdlets and
// aliases that are always read-only. All names are lowercase.
var readOnlyPowerShellCmdlets = map[string]bool{
	// Filesystem reads
	"get-childitem": true, "gci": true, "ls": true, "dir": true,
	"get-content": true, "gc": true, "cat": true, "type": true,
	"get-item": true, "gi": true,
	"get-itemproperty": true,
	"get-itempropertyvalue": true,
	"test-path": true,
	"resolve-path": true,
	"get-filehash": true,
	"get-acl": true,
	"select-string": true,
	"get-location": true, "gl": true, "pwd": true,
	"get-psdrive": true,
	"get-psprovider": true,
	"convert-path": true,
	"join-path": true,
	"split-path": true,

	// Object inspection / transforms (pure)
	"get-member": true, "gm": true,
	"get-unique": true, "gu": true,
	"compare-object": true, "compare": true,
	"join-string": true,
	"get-random": true,
	"convertto-json": true,
	"convertfrom-json": true,
	"convertto-csv": true,
	"convertfrom-csv": true,
	"convertto-xml": true,
	"convertto-html": true,
	"format-hex": true,

	// Pipeline transformers
	"select-object": true, "select": true,
	"sort-object": true, "sort": true,
	"group-object": true, "group": true,
	"where-object": true, "?": true, "where": true,
	"measure-object": true, "measure": true,
	"format-table": true, "ft": true,
	"format-list": true, "fl": true,
	"format-wide": true, "fw": true,
	"format-custom": true, "fc": true,
	"out-string": true,
	"out-host": true,
	"out-null": true,

	// Output
	"write-output": true, "write": true, "echo": true,
	"write-host": true,

	// System info
	"get-process": true, "gps": true, "ps": true,
	"get-service": true, "gsv": true,
	"get-computerinfo": true,
	"get-host": true,
	"get-date": true, "date": true,
	"get-hotfix": true,
	"get-timezone": true,
	"get-uptime": true,
	"get-culture": true,
	"get-uiculture": true,
	"get-alias": true, "gal": true,
	"get-history": true, "h": true, "history": true,

	// Other
	"start-sleep": true, "sleep": true,
}
