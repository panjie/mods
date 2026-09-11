package approval

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// ScriptExecutionFacts describes the one interpreter invocation shape whose
// payload can still be reviewed even though the interpreter cannot be analyzed:
// a bare interpreter program with exactly one literal script-path operand and
// no inline source, encoding, pipeline, redirection, or nested shell.
//
// At the approval layer these facts are only a candidate, and the command stays
// opaque. The app layer fills ResolvedPath, SizeBytes and ContentSHA256 after it
// proves the operand resolves to a readable text file inside the workspace or a
// safe directory; the reviewer then refreshes ContentSHA256 with the digest of
// the bytes it actually displays, and the caller compares the file against that
// digest once more immediately before execution. Until ResolvedPath and
// ContentSHA256 are set the call remains unverifiable and the preflight rejects
// it exactly as before.
type ScriptExecutionFacts struct {
	Interpreter string
	Operand     string

	ResolvedPath  string
	SizeBytes     int64
	ContentSHA256 string
}

// Verified reports whether concrete, displayed script bytes are bound to this
// call. A nil receiver is never verified.
func (f *ScriptExecutionFacts) Verified() bool {
	return f != nil && f.ResolvedPath != "" && f.ContentSHA256 != ""
}

// scriptReviewability keeps interpreter script execution opaque for every
// consumer except the preflight's verified-script exemption. Its effect is
// unbounded, so it must never become a static read, a saveable directory rule,
// or an ordinary compound command; ScriptExecution only records the narrow
// shape the app layer may verify.
func scriptReviewability(facts *ScriptExecutionFacts) CommandReviewability {
	return CommandReviewability{
		Level:           ReviewabilityOpaque,
		Reasons:         []ReviewabilityReason{ReviewabilityScriptExecution},
		ScriptExecution: facts,
	}
}

// scriptExecutionFacts is the shared shape check for direct argv, POSIX AST,
// and PowerShell IR evidence. Requiring a bare program name keeps a
// workspace-local or path-qualified interpreter out of the review path: only
// the script's own bytes are reviewed, not the executable that reads them.
func scriptExecutionFacts(program string, args []string) (*ScriptExecutionFacts, bool) {
	if len(args) != 1 || strings.ContainsAny(strings.TrimSpace(program), `/\`) {
		return nil, false
	}
	interpreter, ok := interpreterProgram(program)
	if !ok {
		return nil, false
	}
	operand := strings.TrimSpace(args[0])
	if !literalScriptOperand(operand) {
		return nil, false
	}
	return &ScriptExecutionFacts{Interpreter: interpreter, Operand: operand}, true
}

// interpreterProgram normalizes a bare program name to the interpreter family
// that carries a script file. Versioned names (python3.12, ruby3.2) belong to
// their family; shell hosts, editors, build tools, and arbitrary executables do
// not, so `sh script.sh` and `bash -c ...` stay opaque.
func interpreterProgram(program string) (string, bool) {
	name := strings.ToLower(path.Base(strings.ReplaceAll(strings.TrimSpace(program), `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	switch name {
	case "py", "node", "nodejs":
		return name, true
	}
	for _, family := range []string{"python", "pypy", "ruby", "perl", "lua", "php"} {
		if name == family || strings.HasPrefix(name, family) {
			return name, true
		}
	}
	return "", false
}

// literalScriptOperand reports whether a value can only denote a file path at
// execution time: no flag prefix, no shell or PowerShell expansion, no
// globbing, redirection, quoting artifact, or control character. The app layer
// still has to prove that the value resolves to a readable file inside the
// workspace or a safe directory, so this check only keeps clearly dynamic or
// inline payloads out of the review path.
func literalScriptOperand(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	if strings.ContainsAny(value, "$`~{}[]*?|&;<>()!\"'") {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n\t")
}

// posixScriptExecution reports the eligible shape for a whole POSIX command:
// one statement, one literal interpreter call, one literal script operand, and
// nothing that could change what runs (no assignments, redirections, negation,
// background execution, or pipeline).
func posixScriptExecution(file *syntax.File) (*ScriptExecutionFacts, bool) {
	if file == nil || len(file.Stmts) != 1 {
		return nil, false
	}
	stmt := file.Stmts[0]
	if stmt == nil || stmt.Cmd == nil || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return nil, false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) != 2 || len(call.Assigns) > 0 {
		return nil, false
	}
	program, ok := staticShellWord(call.Args[0])
	if !ok {
		return nil, false
	}
	operand, ok := staticShellWord(call.Args[1])
	if !ok {
		return nil, false
	}
	return scriptExecutionFacts(program, []string{operand})
}

// powerShellScriptExecution reports the eligible shape for a whole PowerShell
// command: one invocation, one argument, and none of the constructs that hide
// code or change what runs.
func powerShellScriptExecution(ir *psBridgeIR) (*ScriptExecutionFacts, bool) {
	if ir == nil || ir.HasControlFlow || ir.HasScriptBlock || ir.HasBackground ||
		ir.HasStopParsing || ir.HasAssignment || len(ir.Redirects) > 0 || len(ir.Expansions) > 0 ||
		ir.TopLevelStatementCount > 1 || ir.PipelineCount > 1 || len(ir.Invocations) != 1 {
		return nil, false
	}
	for _, flag := range ir.RiskFlags {
		if flag == "invoke_expression" || flag == "syntax_error" {
			return nil, false
		}
	}
	inv := ir.Invocations[0]
	if !isBarePowerShellCommand(inv.Name) || len(inv.Args) != 1 {
		return nil, false
	}
	return scriptExecutionFacts(trimPowerShellLiteral(inv.Name), []string{trimPowerShellLiteral(inv.Args[0])})
}
