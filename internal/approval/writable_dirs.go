package approval

import (
	"path"
	"regexp"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Writable-directory extraction. For a given shell command, determine
// which filesystem paths the command could create, modify, or delete,
// so the reviewer can decide whether the operation falls inside an
// approved directory. POSIX commands go through the mvdan parser;
// PowerShell / Windows commands use the simple tokenizer.

// ExtractWritableDirs returns filesystem directories that a shell command can
// create, modify, or delete. The result is best-effort and may be empty when
// the command is not statically understood.
func ExtractWritableDirs(command string, posix bool) []string {
	return extractWritableDirs(command, posix)
}

func extractWritableDirs(command string, posix bool) []string {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if !posix {
		return extractWritableDirsSimple(normalized)
	}
	return extractWritableDirsPOSIX(command)
}

func extractWritableDirsPOSIX(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	confinementOK := posixFileAllowsScalarConfinement(file)
	var dirs []string
	for _, stmt := range file.Stmts {
		collectWritableDirsFromStmt(stmt, &dirs, confinementOK)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

func collectWritableDirsFromStmt(stmt *syntax.Stmt, dirs *[]string, confinementOK bool) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	for _, redir := range stmt.Redirs {
		if redir == nil || redir.Word == nil || !redirectionWritesPersistent(redir) {
			continue
		}
		target, ok := accessShellWordConfined(redir.Word, confinementOK)
		if !ok || target == "" {
			continue
		}
		*dirs = append(*dirs, parentDir(target))
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		collectWritableDirsFromStmt(binary.X, dirs, confinementOK)
		collectWritableDirsFromStmt(binary.Y, dirs, confinementOK)
		return
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return
	}
	args := shellWordsForAccessConfined(call.Args, confinementOK)
	if len(args) == 0 {
		return
	}
	*dirs = append(*dirs, writableDirsFromTokens(args, true)...)
}

func extractWritableDirsSimple(command string) []string {
	parts := splitSimpleCompound(normalizeSimpleCommand(command))
	if len(parts) == 0 {
		return nil
	}
	var dirs []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dirs = append(dirs, writableDirsFromRedirection(part)...)
		tokens := tokenizeSimple(part)
		if len(tokens) == 0 {
			continue
		}
		dirs = append(dirs, writableDirsFromTokens(tokens, false)...)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

// writableDirsFromTokens maps a tokenized command (args[0] = program
// name) to the directories it could write. The dispatch is a hardcoded
// table of common POSIX utilities and PowerShell cmdlets; anything not
// recognized returns nil (fail-closed at the caller level).
type writableTargetMode uint8

const (
	writableTargetParent writableTargetMode = iota
	writableTargetDirectory
	writableTargetDestination
)

type writableTarget struct {
	path string
	mode writableTargetMode
}

type writableTargetAnalysis struct {
	Known      bool
	Dirs       []string
	Unresolved []string
}

func analyzeWritableTargetsFromTokens(args []string, posix bool) writableTargetAnalysis {
	targets, known := writableTargetsFromTokens(args, posix)
	return analyzeWritableTargets(targets, known, posix, true)
}

func analyzeLiteralWritableTargetsFromTokens(args []string, posix bool) writableTargetAnalysis {
	targets, known := writableTargetsFromTokens(args, posix)
	return analyzeWritableTargets(targets, known, posix, false)
}

func analyzeWritableTargets(targets []writableTarget, known, posix, expandShell bool) writableTargetAnalysis {
	result := writableTargetAnalysis{Known: known}
	for _, target := range targets {
		value := trimPowerShellLiteral(strings.TrimSpace(target.path))
		if value == "" {
			continue
		}
		if expandShell && shellPathExpressionUnresolved(value, posix) {
			result.Unresolved = append(result.Unresolved, value)
			continue
		}
		switch target.mode {
		case writableTargetDirectory:
			result.Dirs = append(result.Dirs, targetDirs([]string{value})...)
		case writableTargetDestination:
			result.Dirs = append(result.Dirs, destinationDir(value))
		default:
			result.Dirs = append(result.Dirs, parentDir(value))
		}
	}
	result.Dirs = dedupeSorted(result.Dirs)
	result.Unresolved = dedupeSorted(result.Unresolved)
	return result
}

func writableDirsFromTokens(args []string, posix bool) []string {
	return analyzeWritableTargetsFromTokens(args, posix).Dirs
}

// writableTargetsFromTokens identifies path-bearing operands before reducing
// them to directories. Keeping this intermediate form lets callers distinguish
// a concrete relative path from a runtime expression instead of turning both
// into ".".
func writableTargetsFromTokens(args []string, posix bool) ([]writableTarget, bool) {
	if len(args) == 0 {
		return nil, false
	}
	command := path.Base(args[0])
	if !posix {
		command = normalizePowerShellCommandName(command)
	}
	parentTargets := func(paths []string) []writableTarget {
		result := make([]writableTarget, 0, len(paths))
		for _, p := range paths {
			result = append(result, writableTarget{path: p, mode: writableTargetParent})
		}
		return result
	}
	dirTargets := func(paths []string) []writableTarget {
		result := make([]writableTarget, 0, len(paths))
		for _, p := range paths {
			result = append(result, writableTarget{path: p, mode: writableTargetDirectory})
		}
		return result
	}
	destinationTargets := func(paths []string) []writableTarget {
		result := make([]writableTarget, 0, len(paths))
		for _, p := range paths {
			result = append(result, writableTarget{path: p, mode: writableTargetDestination})
		}
		return result
	}
	operandsFor := func(values []string) []string {
		if posix {
			return commandOperands(values)
		}
		return powerShellCommandOperands(values)
	}
	firstOperand := func(values []string) []string {
		operands := operandsFor(values)
		if len(operands) == 0 {
			return nil
		}
		return operands[:1]
	}
	switch command {
	case "env":
		nested, ok := envCommandArgs(args[1:])
		if !ok || len(nested) == 0 {
			return nil, false
		}
		return writableTargetsFromTokens(nested, posix)
	case "rm":
		operands := operandsFor(args[1:])
		if removeTargetsAreDirs(args[1:]) {
			return dirTargets(operands), true
		}
		return parentTargets(operands), true
	case "rmdir":
		return dirTargets(operandsFor(args[1:])), true
	case "unlink", "touch", "chmod", "chown":
		return parentTargets(operandsFor(args[1:])), true
	case "mkdir":
		return parentTargets(operandsFor(args[1:])), true
	case "cp", "mv":
		if target := targetDirectoryOption(args[1:]); target != "" {
			// -t names an existing target directory. The command writes inside
			// that directory, rather than replacing the directory entry itself.
			return dirTargets([]string{target}), true
		}
		operands := operandsFor(args[1:])
		if len(operands) == 0 {
			return nil, true
		}
		return destinationTargets(operands[len(operands)-1:]), true
	case "tee":
		return parentTargets(operandsFor(args[1:])), true
	case "find":
		if !findHasWriteAction(args[1:]) {
			return nil, false
		}
		return dirTargets(findRootOperands(args[1:])), true
	case "sort":
		if output := sortOutputPath(args[1:]); output != "" {
			return parentTargets([]string{output}), true
		}
		return nil, sortUsesWritableScratch(args[1:])
	case "git":
		if output := flagValue(args[1:], "--output"); output != "" {
			return parentTargets([]string{output}), true
		}
		return nil, hasAnyArg(args[1:], "--ext-diff", "--textconv")
	case "xxd":
		if !hasAnyArg(args[1:], "-r", "--revert") {
			return nil, false
		}
		operands := commandOperands(args[1:])
		if len(operands) < 2 {
			return nil, true
		}
		return parentTargets(operands[len(operands)-1:]), true
	case "remove-item", "del", "erase", "rd":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentTargets(paths), true
		}
		return parentTargets(operandsFor(args[1:])), true
	case "copy-item", "move-item":
		if destinations := powerShellParamValues(args, "destination"); len(destinations) > 0 {
			return destinationTargets(destinations), true
		}
		operands := operandsFor(args[1:])
		if len(operands) == 0 {
			return nil, true
		}
		return destinationTargets(operands[len(operands)-1:]), true
	case "copy", "move":
		operands := operandsFor(args[1:])
		if len(operands) == 0 {
			return nil, true
		}
		return destinationTargets(operands[len(operands)-1:]), true
	case "new-item", "set-content", "add-content", "clear-content",
		"set-item", "clear-item", "set-itemproperty", "new-itemproperty",
		"remove-itemproperty", "clear-itemproperty", "rename-item":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentTargets(paths), true
		}
		return parentTargets(firstOperand(args[1:])), true
	case "out-file":
		if paths := powerShellParamValues(args, "filepath", "literalpath", "path"); len(paths) > 0 {
			return parentTargets(paths), true
		}
		return parentTargets(firstOperand(args[1:])), true
	default:
		return nil, false
	}
}

func targetDirectoryOption(args []string) string {
	for i, arg := range args {
		switch {
		case arg == "-t" || arg == "--target-directory":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(arg, "--target-directory="):
			return strings.TrimPrefix(arg, "--target-directory=")
		case strings.HasPrefix(arg, "-t") && len(arg) > 2:
			return strings.TrimPrefix(arg, "-t")
		}
	}
	return ""
}

func shellPathExpressionUnresolved(value string, posix bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	if posix {
		if strings.HasPrefix(value, "$HOME/") || strings.HasPrefix(value, "${HOME}/") {
			return false
		}
		return strings.ContainsAny(value, "$`")
	}
	for _, prefix := range []string{"$home\\", "$home/", "${home}\\", "${home}/", "$env:userprofile\\", "$env:userprofile/", "${env:userprofile}\\", "${env:userprofile}/"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	if strings.Contains(value, "$(") || strings.Contains(value, "@(") {
		return true
	}
	// A leading "(" is a runtime subexpression only when its parentheses
	// actually close (for example "(Get-Location).Path"). An unbalanced
	// fragment such as `(progn (message \` — a literal from a mis-parsed
	// POSIX --eval argument — is data, not a runtime target.
	if strings.HasPrefix(value, "(") && strings.Contains(value, ")") {
		return true
	}
	if strings.Contains(value, "$") || strings.HasPrefix(value, "@") {
		return true
	}
	// Only cmd.exe variable syntax (%NAME%) is a runtime target; a lone "%"
	// (a printf format such as %s, or a literal percent) is not.
	if reCMDLocalVar.MatchString(value) && !strings.HasPrefix(lower, "%userprofile%") && !strings.HasPrefix(lower, "%homedrive%%homepath%") {
		return true
	}
	return false
}

var reCMDLocalVar = regexp.MustCompile(`%[A-Za-z_][A-Za-z0-9_]*%`)

// IsUnresolvedShellPathExpression reports whether a path-like value still
// depends on shell runtime evaluation. Approval code must never normalize such
// a value relative to the workspace or persist it in a directory rule.
func IsUnresolvedShellPathExpression(value string, posix bool) bool {
	return shellPathExpressionUnresolved(value, posix)
}

// powerShellArgQuoting reports whether an argument token from the PowerShell
// IR is a single- or double-quoted string literal in the command source. The
// IR preserves the quotes; trimPowerShellLiteral strips them later.
func powerShellArgQuoting(arg string) (single, double bool) {
	if len(arg) < 2 {
		return false, false
	}
	switch arg[0] {
	case '\'':
		return arg[len(arg)-1] == '\'', false
	case '"':
		return false, arg[len(arg)-1] == '"'
	default:
		return false, false
	}
}

// shellPathExpressionUnresolvedQuoted applies the PowerShell unresolved-path
// heuristics with knowledge of the argument's quoting in the command source.
// A single-quoted string never interpolates, and inside a double-quoted string
// only $-prefixed expansions are runtime-evaluated: a leading "(" without a
// preceding "$" is literal data (for example elisp passed to emacs --eval),
// not a subexpression. Unquoted arguments keep the full heuristics so a real
// subexpression such as (Get-Location).Path stays dynamic.
func shellPathExpressionUnresolvedQuoted(value string, single, double bool) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	lower := strings.ToLower(value)
	for _, prefix := range []string{"$home\\", "$home/", "${home}\\", "${home}/", "$env:userprofile\\", "$env:userprofile/", "${env:userprofile}\\", "${env:userprofile}/"} {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}
	if single {
		return isCMDLocalVarTarget(value, lower)
	}
	if double {
		if strings.Contains(value, "$") {
			return true
		}
		return isCMDLocalVarTarget(value, lower)
	}
	return shellPathExpressionUnresolved(value, false)
}

// isCMDLocalVarTarget reports whether value references cmd.exe variable syntax
// (%NAME%). A lone "%" (a printf format such as %s, or a literal percent) is
// not a runtime target.
func isCMDLocalVarTarget(value, lower string) bool {
	return reCMDLocalVar.MatchString(value) && !strings.HasPrefix(lower, "%userprofile%") && !strings.HasPrefix(lower, "%homedrive%%homepath%")
}

func flagValue(args []string, name string) string {
	for i, arg := range args {
		if value, ok := strings.CutPrefix(arg, name+"="); ok {
			return value
		}
		if arg == name && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

func hasKnownRiskyInvocation(args []string, posix bool) bool {
	if len(args) == 0 {
		return false
	}
	name := path.Base(args[0])
	if !posix {
		name = strings.ToLower(name)
	}
	switch name {
	case "env":
		nested, ok := envCommandArgs(args[1:])
		return ok && hasKnownRiskyInvocation(nested, posix)
	case "rm", "rmdir", "unlink", "touch", "chmod", "chown", "mkdir", "cp", "mv", "tee":
		return true
	case "find":
		return findHasWriteAction(args[1:])
	case "sort":
		return sortOutputPath(args[1:]) != "" || sortUsesWritableScratch(args[1:])
	case "xargs":
		nested, ok := xargsCommandArgs(args[1:])
		return ok && hasKnownRiskyInvocation(nested, posix)
	case "git":
		return hasAnyArg(args[1:], "--output", "--ext-diff", "--textconv")
	case "xxd":
		return hasAnyArg(args[1:], "-r", "--revert")
	default:
		return false
	}
}

func hasKnownRiskyShellCommand(command string, posix bool) bool {
	if !posix {
		for _, part := range splitSimpleCompound(normalizeSimpleCommand(command)) {
			if hasKnownRiskyInvocation(tokenizeSimple(part), false) {
				return true
			}
		}
		return false
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return false
	}
	risky := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if risky {
			return false
		}
		if exp, ok := node.(*syntax.ParamExp); ok {
			// Runtime-expanded arguments may resolve to external paths that the
			// approval matrix cannot derive from the command text.
			if _, known := simpleHomeExpansion(exp); !known {
				risky = true
				return false
			}
			return true
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		if args := shellWordsForAccess(call.Args); len(args) > 0 && hasKnownRiskyInvocation(args, true) {
			risky = true
			return false
		}
		return true
	})
	return risky
}

func findHasWriteAction(args []string) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-delete", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		case "-exec", "-execdir":
			nested, next, ok := findExecCommand(args, i+1)
			if !ok {
				return true
			}
			if readOnly, _ := invocationTokensReadOnly(nested, ReadOnlyCommandPolicy{}); !readOnly {
				return true
			}
			i = next - 1
		}
	}
	return false
}

func findRootOperands(args []string) []string {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-H", "-L", "-P":
			i++
		default:
			goto roots
		}
	}
roots:
	var paths []string
	for i < len(args) && !findExpressionToken(args[i]) {
		paths = append(paths, args[i])
		i++
	}
	if len(paths) == 0 {
		return []string{"."}
	}
	return paths
}

func sortOutputPath(args []string) string {
	for i, arg := range args {
		switch {
		case arg == "-o" || arg == "--output":
			if i+1 < len(args) {
				return args[i+1]
			}
		case strings.HasPrefix(arg, "--output="):
			return strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-o") && len(arg) > 2:
			return strings.TrimPrefix(arg, "-o")
		}
	}
	return ""
}

func sortUsesWritableScratch(args []string) bool {
	for _, arg := range args {
		if arg == "-T" || strings.HasPrefix(arg, "-T") ||
			arg == "--temporary-directory" || strings.HasPrefix(arg, "--temporary-directory=") ||
			arg == "--compress-program" || strings.HasPrefix(arg, "--compress-program=") {
			return true
		}
	}
	return false
}

func powerShellParamValues(args []string, names ...string) []string {
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[strings.ToLower(name)] = struct{}{}
	}
	var values []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		key := strings.TrimLeft(arg, "-")
		var inlineValue string
		if before, after, ok := strings.Cut(key, ":"); ok {
			key = before
			inlineValue = after
		}
		if _, ok := nameSet[strings.ToLower(key)]; !ok {
			continue
		}
		if inlineValue != "" {
			values = append(values, inlineValue)
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func commandOperands(args []string) []string {
	operands := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

// powerShellCommonValueParameters are the engine common parameters every
// cmdlet accepts whose argument is an action preference or a variable name,
// never a filesystem path. Abbreviated parameter names stay unhandled on
// purpose: missing a skip only leaves a bogus operand, while skipping a real
// path would under-scope the review.
var powerShellCommonValueParameters = map[string]bool{
	"erroraction":         true,
	"warningaction":       true,
	"informationaction":   true,
	"progressaction":      true,
	"errorvariable":       true,
	"warningvariable":     true,
	"informationvariable": true,
	"pipelinevariable":    true,
	"outvariable":         true,
	"outbuffer":           true,
}

// powerShellCommandOperands extracts path operands the way commandOperands
// does, additionally skipping the argument that follows a value-taking
// common parameter (-ErrorAction SilentlyContinue): treating such a value as
// a path operand yields a bogus "." write target.
func powerShellCommandOperands(args []string) []string {
	operands := make([]string, 0, len(args))
	skipValue := false
	for _, arg := range args {
		if skipValue {
			skipValue = false
			continue
		}
		if arg == "" {
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			if strings.Contains(arg, ":") {
				continue // inline form such as -ErrorAction:SilentlyContinue
			}
			skipValue = powerShellCommonValueParameters[strings.TrimLeft(strings.ToLower(arg), "-")]
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

func removeTargetsAreDirs(args []string) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if strings.HasPrefix(arg, "--recursive") || arg == "--dir" {
			return true
		}
		if strings.HasPrefix(arg, "-") && strings.ContainsAny(strings.TrimLeft(arg, "-"), "rR") {
			return true
		}
	}
	return false
}

func targetDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		trimmed := strings.TrimRight(path, `/\`)
		if trimmed == "" {
			trimmed = path
		}
		dirs = append(dirs, trimmed)
	}
	return dirs
}

func writableDirsFromRedirection(command string) []string {
	tokens := tokenizeSimple(command)
	dirs := make([]string, 0)
	for i, token := range tokens {
		if !isRedirectionToken(token) || i+1 >= len(tokens) {
			continue
		}
		if isNullRedirectionTarget(tokens[i+1]) {
			continue
		}
		dirs = append(dirs, parentDir(tokens[i+1]))
	}
	return dirs
}
