package commandanalysis

import (
	"regexp"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// Writable targets are extracted from literal operands supplied by the
// POSIX AST, PowerShell bridge IR, or direct argv assessment.

// Writable target analysis maps a tokenized command (args[0] = program
// name) to the directories it could write. The dispatch uses the unified
// catalog of common POSIX utilities and PowerShell cmdlets; anything not
// recognized returns nil (fail-closed at the caller level).
type writableTargetMode uint8

const (
	writableTargetParent writableTargetMode = iota
	writableTargetDirectory
	writableTargetDestination
	writableTargetRuntime
)

type writableTarget struct {
	path string
	mode writableTargetMode
}

type writableTargetAnalysis struct {
	Known           bool
	Dirs            []string
	Unresolved      []string
	ProviderTargets []string
}

func analyzeWritableTargetsFromTokens(args []string, posix bool, options ...pathutil.Options) writableTargetAnalysis {
	targets, known := writableTargetsFromTokens(args, posix)
	return analyzeWritableTargets(targets, known, posix, true, options...)
}

func analyzeLiteralWritableTargetsFromTokens(args []string, posix bool) writableTargetAnalysis {
	targets, known := writableTargetsFromTokens(args, posix)
	return analyzeWritableTargets(targets, known, posix, false)
}

func analyzeWritableTargets(targets []writableTarget, known, posix, expandShell bool, options ...pathutil.Options) writableTargetAnalysis {
	result := writableTargetAnalysis{Known: known}
	for _, target := range targets {
		value := trimPowerShellLiteral(strings.TrimSpace(target.path))
		if value == "" {
			continue
		}
		if target.mode == writableTargetRuntime {
			result.Unresolved = append(result.Unresolved, value)
			continue
		}
		if expandShell && shellPathExpressionUnresolved(value, posix) {
			result.Unresolved = append(result.Unresolved, value)
			continue
		}
		if !posix && IsPowerShellProviderPath(value) {
			result.ProviderTargets = append(result.ProviderTargets, value)
			continue
		}
		if !posix {
			if filesystemPath, ok := PowerShellFilesystemProviderPath(value); ok {
				value = filesystemPath
			}
		}
		switch target.mode {
		case writableTargetDirectory:
			result.Dirs = append(result.Dirs, targetDirs([]string{value})...)
		case writableTargetDestination:
			dir := destinationDir(value)
			if expandShell && len(options) > 0 && (strings.HasSuffix(value, "/") || strings.HasSuffix(value, `\`)) {
				dir = pathutil.ExpandToken(dir, options[0])
			}
			result.Dirs = append(result.Dirs, dir)
		default:
			result.Dirs = append(result.Dirs, parentDir(value))
		}
	}
	result.Dirs = dedupeSorted(result.Dirs)
	result.Unresolved = dedupeSorted(result.Unresolved)
	result.ProviderTargets = dedupeSorted(result.ProviderTargets)
	return result
}

// IsPowerShellProviderPath reports whether value names a PowerShell provider
// path rather than a native filesystem path. Named drives cannot safely become
// filesystem directory rules: their provider can be Registry, Certificate,
// WSMan, or a custom provider selected by the PowerShell session.
func IsPowerShellProviderPath(value string) bool {
	value = normalizePowerShellProviderSyntax(value)
	if value == "" {
		return false
	}
	if qualifier, _, ok := strings.Cut(value, "::"); ok {
		provider := powerShellProviderBaseName(qualifier)
		return validPowerShellProviderName(provider) && !strings.EqualFold(provider, "filesystem")
	}
	colon := strings.IndexByte(value, ':')
	if colon < 1 || !validPowerShellProviderName(value[:colon]) {
		return false
	}
	if colon == 1 {
		return false
	}
	drive := strings.ToLower(value[:colon])
	switch drive {
	case "alias", "cert", "env", "function", "hkcu", "hklm", "variable", "wsman":
		return true
	}
	return true
}

// PowerShellFilesystemProviderPath removes an explicit FileSystem provider
// qualifier so the remaining native path can use ordinary directory rules.
func PowerShellFilesystemProviderPath(value string) (string, bool) {
	value = normalizePowerShellProviderSyntax(value)
	qualifier, target, ok := strings.Cut(value, "::")
	if !ok || !strings.EqualFold(powerShellProviderBaseName(qualifier), "filesystem") || target == "" {
		return "", false
	}
	return target, true
}

func normalizePowerShellProviderSyntax(value string) string {
	value = trimPowerShellLiteral(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "`:", ":")
	if colon := strings.IndexByte(value, ':'); colon > 0 {
		value = strings.ReplaceAll(value[:colon], "`", "") + value[colon:]
	}
	return value
}

func powerShellProviderBaseName(value string) string {
	if i := strings.LastIndexAny(value, `/\\`); i >= 0 {
		return value[i+1:]
	}
	return value
}

func validPowerShellProviderName(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.' {
			continue
		}
		return false
	}
	return true
}

// writableTargetsFromTokens identifies path-bearing operands before reducing
// them to directories. Keeping this intermediate form lets callers distinguish
// a concrete relative path from a runtime expression instead of turning both
// into ".".
func writableTargetsFromTokens(args []string, posix bool) ([]writableTarget, bool) {
	f := catalogTokens(args, spaceForShell(posix), ReadOnlyCommandPolicy{})
	return f.Writes, f.KnownWrite
}
func canonicalPowerShellWriteCommand(name string) string {
	e := commandCatalog.lookup(powershellSpace, name)
	if e != nil {
		return e.Name
	}
	return name
}

func powerShellMutatesPathContext(ir *psBridgeIR) bool {
	if ir == nil {
		return false
	}
	for _, inv := range ir.Invocations {
		switch normalizePowerShellCommandName(inv.Name) {
		case "new-psdrive", "mount", "ndr", "remove-psdrive", "rdr",
			"set-location", "cd", "chdir", "sl", "push-location", "pushd", "pop-location", "popd":
			return true
		}
	}
	return false
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
// a value relative to the cwd or persist it in a directory rule.
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
	return catalogTokens(args, spaceForShell(posix), ReadOnlyCommandPolicy{}).Risky
}

func findHasWriteAction(args []string, ev *catalogEvaluation) bool {
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "-delete", "-ok", "-okdir", "-fprint", "-fprint0", "-fprintf", "-fls":
			return true
		case "-exec", "-execdir":
			nested, next, ok := findExecCommand(args, i+1)
			if !ok {
				return true
			}
			if !findExecReadOnly(nested, args[next-1] == "+", ev) {
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
		if !powerShellParameterMatches(strings.ToLower(key), nameSet) {
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

func powerShellParameterMatches(key string, names map[string]struct{}) bool {
	if key == "" {
		return false
	}
	if _, ok := names[key]; ok {
		return true
	}
	if _, ok := names["literalpath"]; ok {
		if key == "lp" || strings.HasPrefix("literalpath", key) || len(key) >= 4 && strings.HasPrefix("pspath", key) {
			return true
		}
	}
	if _, ok := names["path"]; ok && len(key) >= 3 && strings.HasPrefix("path", key) {
		return true
	}
	if _, ok := names["destination"]; ok && len(key) >= 1 && strings.HasPrefix("destination", key) {
		return true
	}
	if _, ok := names["filepath"]; ok && len(key) >= 2 && strings.HasPrefix("filepath", key) {
		return true
	}
	return false
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

var powerShellCommandValueParameters = map[string]bool{
	"aclobject":    true,
	"credential":   true,
	"encoding":     true,
	"filter":       true,
	"include":      true,
	"itemtype":     true,
	"name":         true,
	"newname":      true,
	"propertytype": true,
	"value":        true,
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
			name := strings.TrimLeft(strings.ToLower(arg), "-")
			skipValue = powerShellCommonValueParameters[name] || powerShellCommandValueParameters[name]
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

func removeTargetsAreDirs(args []string, posix bool) bool {
	for _, arg := range args {
		if arg == "--" {
			return false
		}
		if !posix {
			if !strings.HasPrefix(arg, "-") {
				continue
			}
			name := strings.TrimLeft(strings.ToLower(arg), "-")
			if name == "rf" || len(name) >= 1 && strings.HasPrefix("recurse", name) {
				return true
			}
			continue
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
