package approval

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Pre-written script payloads are exempt from the advisory simplification
// nudges. A command whose payload is a file — one interpreter or shell host with
// a literal script path, or the path to a script itself — runs unchanged: the
// reviewer can inspect the file, and rewriting it is exactly how pre-written
// skill scripts break.
//
// Ad-hoc generated source (inline `-c`/`-e`, encoded payloads, compound
// one-liners) keeps the nudges, because its text is the only thing there is to
// review. This is deliberately a syntactic test with no filesystem access: it
// must behave identically for scripts the app cannot resolve (a skill directory
// outside the workspace), and it must never become a path-authorization
// decision.

// scriptFileSuffixes are the file extensions that identify a program operand as
// a script file rather than an executable or a data argument.
var scriptFileSuffixes = []string{
	".sh", ".bash", ".zsh", ".ksh", ".fish",
	".ps1", ".psm1",
	".py", ".rb", ".pl", ".lua", ".php",
	".js", ".mjs", ".cjs", ".ts",
	".bat", ".cmd",
}

func hasScriptFileSuffix(name string) bool {
	for _, suffix := range scriptFileSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// inlinePayloadFlags mark arguments that carry source in the invocation itself
// instead of naming a file. Their presence disqualifies the payload exemption.
var inlinePayloadFlags = map[string]bool{
	"-c": true, "-e": true, "-r": true, "-m": true,
	"-command": true, "-encodedcommand": true,
	"--eval": true, "--eval=": true,
	"/c": true,
}

// payloadHostProgram reports whether a program runs a script named by its
// operand: a shell host, or an interpreter that reads a source file.
func payloadHostProgram(program string) bool {
	name := programBaseName(program)
	if shellHostPrograms[name] {
		return true
	}
	switch name {
	case "py", "node", "nodejs":
		return true
	}
	for _, family := range []string{"python", "pypy", "ruby", "perl", "lua", "php"} {
		if name == family || strings.HasPrefix(name, family) {
			return true
		}
	}
	return false
}

func programBaseName(program string) string {
	name := strings.ToLower(path.Base(strings.ReplaceAll(strings.TrimSpace(program), `\`, "/")))
	return strings.TrimSuffix(name, ".exe")
}

// literalScriptPath reports whether a value can only denote a file path at
// execution time. Dynamic expansions, globs, quoting artifacts, and control
// characters disqualify it; the fact that the file exists is not required,
// because this flag only decides whether the model is nudged.
func literalScriptPath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || strings.HasPrefix(value, "-") {
		return false
	}
	if strings.ContainsAny(value, "$`~{}[]*?|&;<>()!\"'") {
		return false
	}
	return !strings.ContainsAny(value, "\x00\r\n\t")
}

// scriptFilePayload is the shared argv shape test: either the host program names
// a script file among its arguments and no argument carries inline source, or
// the program is itself a script path.
func scriptFilePayload(program string, args []string) bool {
	if hasScriptFileSuffix(programBaseName(program)) {
		return true
	}
	if !payloadHostProgram(program) {
		return false
	}
	fileOperand := false
	for _, arg := range args {
		value := strings.ToLower(strings.TrimSpace(arg))
		if inlinePayloadFlags[value] || strings.HasPrefix(value, "--eval=") {
			return false
		}
		if literalScriptPath(arg) && (hasScriptFileSuffix(programBaseName(arg)) ||
			strings.ContainsAny(arg, `/\`) || strings.Contains(arg, ".")) {
			fileOperand = true
		}
	}
	return fileOperand
}

// posixScriptFilePayload reports the whole-command shape for POSIX source: one
// statement, one literal program, and no construct that changes what runs.
func posixScriptFilePayload(file *syntax.File) bool {
	if file == nil || len(file.Stmts) != 1 {
		return false
	}
	stmt := file.Stmts[0]
	if stmt == nil || stmt.Cmd == nil || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) > 0 {
		return false
	}
	program, ok := staticShellWord(call.Args[0])
	if !ok {
		return false
	}
	args := make([]string, 0, len(call.Args)-1)
	for _, word := range call.Args[1:] {
		value, ok := staticShellWord(word)
		if !ok {
			return false
		}
		args = append(args, value)
	}
	return scriptFilePayload(program, args)
}

// powerShellScriptFilePayload reports the whole-command shape for PowerShell IR.
func powerShellScriptFilePayload(ir *psBridgeIR) bool {
	if ir == nil || ir.HasControlFlow || ir.HasScriptBlock || ir.HasBackground ||
		ir.HasStopParsing || ir.HasAssignment || len(ir.Redirects) > 0 || len(ir.Expansions) > 0 ||
		ir.TopLevelStatementCount > 1 || ir.PipelineCount > 1 || len(ir.Invocations) != 1 {
		return false
	}
	for _, flag := range ir.RiskFlags {
		if flag == "invoke_expression" || flag == "syntax_error" {
			return false
		}
	}
	inv := ir.Invocations[0]
	args := make([]string, 0, len(inv.Args))
	for _, arg := range inv.Args {
		args = append(args, trimPowerShellLiteral(arg))
	}
	return scriptFilePayload(trimPowerShellLiteral(inv.Name), args)
}
