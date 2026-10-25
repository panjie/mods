package commandanalysis

import (
	"slices"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// IsReadOnlyPOSIX analyzes a POSIX shell command using the mvdan.cc/sh AST
// and reports whether it is definitively read-only. Returns (true, reason)
// when read-only; (false, "") when not or inconclusive (fail-closed).
func IsReadOnlyPOSIX(command string) (bool, string) {
	return IsReadOnlyPOSIXWithPolicy(command, ReadOnlyCommandPolicy{})
}

// IsReadOnlyPOSIXWithPolicy is IsReadOnlyPOSIX with additional command names
// the user explicitly trusts as read-only.
func IsReadOnlyPOSIXWithPolicy(command string, policy ReadOnlyCommandPolicy) (bool, string) {
	assessment := AssessShellStaticWithPolicy(command, true, policy)
	return assessment.Effect == EffectRead, assessment.Reason
}

// stmtIsReadOnly checks a single statement: background, redirects, then
// delegates to the command-level check.
func stmtIsReadOnly(stmt *syntax.Stmt, policy ReadOnlyCommandPolicy) (bool, string) {
	if stmt == nil || stmt.Cmd == nil {
		return false, ""
	}
	if stmt.Background {
		return false, ""
	}
	for _, redir := range stmt.Redirs {
		if redir == nil {
			continue
		}
		if redirectionWritesPersistent(redir) {
			return false, ""
		}
		if redir.Word != nil && wordHasProcSubst(redir.Word) {
			return false, ""
		}
	}
	return cmdIsReadOnly(stmt.Cmd, policy)
}

// cmdIsReadOnly dispatches on command type. BinaryCmd and Subshell recurse;
// CallExpr does leaf classification; everything else is fail-closed.
func cmdIsReadOnly(cmd syntax.Command, policy ReadOnlyCommandPolicy) (bool, string) {
	switch c := cmd.(type) {
	case *syntax.BinaryCmd:
		if ro, _ := stmtIsReadOnly(c.X, policy); !ro {
			return false, ""
		}
		return stmtIsReadOnly(c.Y, policy)
	case *syntax.Subshell:
		for _, stmt := range c.Stmts {
			if ro, _ := stmtIsReadOnly(stmt, policy); !ro {
				return false, ""
			}
		}
		return true, "read-only subshell"
	case *syntax.ForClause:
		return policy.session.boundedLoop(c).read, "bounded read-only loop"
	case *syntax.CallExpr:
		return callIsReadOnly(c, policy)
	default:
		return false, ""
	}
}

// callIsReadOnly classifies a leaf command: checks word parts for dynamic
// constructs, extracts the command name, then checks the allowlist or
// subcommand table.
func callIsReadOnly(call *syntax.CallExpr, policy ReadOnlyCommandPolicy) (bool, string) {
	if call == nil || len(call.Args) == 0 {
		return false, ""
	}
	// Prefix assignments can change executable resolution and inject runtime or
	// provider-specific hooks (for example PATH, LD_PRELOAD, or
	// GIT_EXTERNAL_DIFF). Leave them to the reviewed classifier instead of
	// treating the command that follows as statically read-only.
	if len(call.Assigns) > 0 {
		return false, ""
	}
	for _, arg := range call.Args {
		if !wordIsReadOnly(arg, policy) {
			return false, ""
		}
	}
	name, ok := staticShellWord(call.Args[0])
	if !ok || !isBarePOSIXCommand(name) {
		return false, ""
	}
	session := policy.session
	if session == nil {
		session = newSemanticSession(nil, policy)
	}
	return invocationRead(session.posix(call), policy)
}

// invocationTokensReadOnly classifies one statically tokenized command. It
// validates arguments as well as the executable name so wrappers and
// output-producing flags cannot inherit a read-only classification merely
// from their first token.
func invocationTokensReadOnly(args []string, policy ReadOnlyCommandPolicy) (bool, string) {
	f := catalogTokens(args, nativeSpace, policy)
	return f.ReadOnly, f.ReadReason
}

func findInvocationReadOnly(args []string, ev *catalogEvaluation) bool {
	i := 0
	for i < len(args) {
		switch args[i] {
		case "-H", "-L", "-P":
			i++
		default:
			goto paths
		}
	}

paths:
	for i < len(args) && !findExpressionToken(args[i]) {
		if strings.HasPrefix(args[i], "-") {
			return false
		}
		i++
	}
	for i < len(args) {
		arg := args[i]
		switch arg {
		case "!", "-not", "(", ")", "-a", "-and", "-o", "-or",
			"-print", "-print0", "-prune", "-true", "-false", "-empty",
			"-nouser", "-nogroup":
			i++
		case "-type", "-name", "-iname", "-path", "-ipath", "-regex", "-iregex",
			"-mtime", "-mmin", "-atime", "-amin", "-ctime", "-cmin", "-size",
			"-maxdepth", "-mindepth", "-user", "-group", "-perm", "-links",
			"-inum", "-newer", "-fstype":
			if i+1 >= len(args) {
				return false
			}
			i += 2
		case "-exec", "-execdir":
			nested, next, ok := findExecCommand(args, i+1)
			if !ok {
				return false
			}
			if !findExecReadOnly(nested, args[next-1] == "+", ev) {
				return false
			}
			i = next
		default:
			return false
		}
	}
	return true
}

// findExecCommand returns the literal argv between -exec/-execdir and its
// required terminator. The nested command is classified independently so a
// reader such as `-exec cat {} +` does not inherit the risk of arbitrary
// `find -exec` calls.
func findExecCommand(args []string, start int) (nested []string, next int, ok bool) {
	for i := start; i < len(args); i++ {
		if args[i] != ";" && args[i] != "+" {
			continue
		}
		if i == start {
			return nil, 0, false
		}
		return args[start:i], i + 1, true
	}
	return nil, 0, false
}

func findExpressionToken(arg string) bool {
	return arg == "!" || arg == "(" || arg == ")" || strings.HasPrefix(arg, "-")
}

// sedInvocationReadOnly reports whether a sed invocation is provably
// read-only. GNU sed can write files through the w/W script commands and
// in-place editing (-i), and scripts loaded from a file (-f) are unknowable.
// The analysis rejects in-place and script-file forms plus any unknown
// option, and requires every script argument (from -e/--expression or the
// first bare operand) to contain no "w" or "W" — a pattern such as s/hw/e/
// merely stays unproven and falls back to the classifier, which is
// fail-closed. Input file operands are unrestricted because output never
// targets them.
func sedInvocationReadOnly(args []string) bool {
	scriptSeen := false
	options := true
	operands := 0
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !options || arg == "--" || arg == "-" || !strings.HasPrefix(arg, "-") {
			if arg == "--" {
				options = false
				continue
			}
			operands++
			if operands == 1 && !scriptSeen {
				if strings.ContainsAny(arg, "wW") {
					return false
				}
				scriptSeen = true
			}
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name, value, hasValue := strings.Cut(arg, "=")
			switch name {
			case "--quiet", "--silent", "--regexp-extended", "--separate",
				"--unbuffered", "--null-data", "--binary", "--posix", "--debug":
			case "--expression":
				if !hasValue {
					if i+1 >= len(args) {
						return false
					}
					i++
					value = args[i]
				}
				if strings.ContainsAny(value, "wW") {
					return false
				}
				scriptSeen = true
			default:
				return false
			}
			continue
		}
		short := strings.TrimPrefix(arg, "-")
		for len(short) > 0 {
			option := short[0]
			short = short[1:]
			switch option {
			case 'n', 'E', 'r', 's', 'u', 'z', 'b':
			case 'e':
				script := short
				short = ""
				if script == "" {
					if i+1 >= len(args) {
						return false
					}
					i++
					script = args[i]
				}
				if strings.ContainsAny(script, "wW") {
					return false
				}
				scriptSeen = true
			default:
				return false
			}
		}
	}
	return scriptSeen
}

func sortInvocationReadOnly(args []string) bool {
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !options || arg == "-" || !strings.HasPrefix(arg, "-") {
			continue
		}
		if arg == "--" {
			options = false
			continue
		}
		if strings.HasPrefix(arg, "--") {
			name, _, hasValue := strings.Cut(arg, "=")
			switch name {
			case "--ignore-leading-blanks", "--check", "--dictionary-order",
				"--ignore-case", "--general-numeric-sort", "--ignore-nonprinting",
				"--month-sort", "--human-numeric-sort", "--numeric-sort",
				"--random-sort", "--reverse", "--stable", "--unique",
				"--version-sort", "--zero-terminated", "--merge":
				continue
			case "--key", "--field-separator":
				if !hasValue {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				continue
			default:
				return false
			}
		}
		short := strings.TrimPrefix(arg, "-")
		for len(short) > 0 {
			option := short[0]
			short = short[1:]
			if strings.ContainsRune("bcCdfghimMnRrsuVz", rune(option)) {
				continue
			}
			if option == 'k' || option == 't' {
				if short == "" {
					if i+1 >= len(args) {
						return false
					}
					i++
				}
				short = ""
				continue
			}
			return false
		}
	}
	return true
}

func xargsInvocationReadOnly(args []string) bool {
	nested, ok := xargsCommandArgs(args)
	if !ok || len(nested) == 0 {
		return false
	}
	name := nested[0]
	if !isBarePOSIXCommand(name) {
		return false
	}
	if !catalogNameOnlyRead(nativeSpace, name) {
		return false
	}
	return true
}

func xargsCommandArgs(args []string) ([]string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch arg {
		case "--":
			if i+1 >= len(args) {
				return nil, false
			}
			return args[i+1:], true
		case "-0", "--null", "-x", "--exit", "-t", "--verbose",
			"-r", "--no-run-if-empty":
			continue
		case "-n", "--max-args", "-L", "--max-lines", "-s", "--max-chars":
			if i+1 >= len(args) {
				return nil, false
			}
			i++
			continue
		}
		if strings.HasPrefix(arg, "--max-args=") ||
			strings.HasPrefix(arg, "--max-lines=") ||
			strings.HasPrefix(arg, "--max-chars=") ||
			shortXargsOptionWithValue(arg, 'n') ||
			shortXargsOptionWithValue(arg, 'L') ||
			shortXargsOptionWithValue(arg, 's') {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			return nil, false
		}
		return args[i:], true
	}
	return nil, false
}

func shortXargsOptionWithValue(arg string, option byte) bool {
	return len(arg) > 2 && arg[0] == '-' && arg[1] == option
}

func envInvocationReadOnly(args []string, policy ReadOnlyCommandPolicy, ev *catalogEvaluation) (bool, string) {
	nested, ok := envCommandArgs(args)
	if !ok {
		return false, ""
	}
	if len(nested) == 0 {
		return true, "read-only command: env"
	}
	f := ev.processTokens(nested, nativeSpace)
	return invocationRead(&f, policy)
}

func envCommandArgs(args []string) ([]string, bool) {
	unsafeAssignment := false
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			if len(args) == 1 || unsafeAssignment {
				return nil, false
			}
			return args[1:], true
		case isEnvAssignment(arg):
			unsafeAssignment = unsafeAssignment || !readOnlyEnvAssignment(arg)
			args = args[1:]
		case strings.HasPrefix(arg, "-"):
			// Options such as -S/--split-string can hide another command and
			// require option-aware parsing. Treat them as unknown.
			return nil, false
		default:
			if unsafeAssignment {
				return nil, false
			}
			return args, true
		}
	}
	return nil, true
}

func readOnlyEnvAssignment(arg string) bool {
	name, _, ok := strings.Cut(arg, "=")
	if !ok {
		return false
	}
	return name == "LANG" || name == "LANGUAGE" || name == "TZ" ||
		name == "TERM" || name == "COLORTERM" || name == "NO_COLOR" ||
		name == "CLICOLOR" || strings.HasPrefix(name, "LC_")
}

func isEnvAssignment(arg string) bool {
	name, _, ok := strings.Cut(arg, "=")
	if !ok || name == "" {
		return false
	}
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func readSubcommandEntry(entry *commandEntry, args []string) bool {
	name := entry.Name
	if name == "git" {
		var ok bool
		args, _, ok = bindGitGlobals(args, "")
		if !ok {
			return false
		}
	}
	if len(args) == 0 {
		return false
	}
	if name == "git" && gitDiscoveryReadOnly(args) {
		return true
	}
	if name == "git" && args[0] == "reflog" {
		// Explicit query forms only; expire/delete/drop/write mutate refs.
		return len(args) == 1 || args[1] == "show" && !hasAnyArg(args[2:], "--output", "--ext-diff", "--textconv")
	}
	subcmd := strings.ToLower(args[0])
	if subcmd == "" || strings.HasPrefix(subcmd, "-") || !slices.Contains(entry.Subcommands, subcmd) {
		return false
	}
	if name == "git" && hasAnyArg(args[1:], "--output", "--ext-diff", "--textconv") {
		return false
	}
	if name == "go" {
		switch subcmd {
		case "vet":
			if hasAnyArg(args[1:], "-vettool") {
				return false
			}
		case "list":
			if hasAnyArg(args[1:], "-toolexec") {
				return false
			}
		}
	}
	// kubectl diff honors KUBECTL_EXTERNAL_DIFF from the inherited process
	// environment, which the command text classifier cannot verify.
	if name == "kubectl" && subcmd == "diff" {
		return false
	}
	return true
}

// gitDiscoveryReadOnly recognizes local repository/configuration queries. In
// particular remote and config cannot be allowed by subcommand name alone:
// both also support writes.
func gitDiscoveryReadOnly(args []string) bool {
	switch args[0] {
	case "branch":
		for _, arg := range args[1:] {
			switch arg {
			case "--show-current", "-v", "-vv", "--verbose", "-a", "--all", "-r", "--remotes", "--list", "-l":
			default:
				return false
			}
		}
		return true
	case "remote":
		if len(args) == 1 || len(args) == 2 && (args[1] == "-v" || args[1] == "--verbose") {
			return true
		}
		if len(args) < 3 || args[1] != "get-url" {
			return false
		}
		operands := 0
		for _, arg := range args[2:] {
			if arg == "--push" || arg == "--all" {
				continue
			}
			if strings.HasPrefix(arg, "-") {
				return false
			}
			operands++
		}
		return operands == 1
	case "config":
		action, operands := "", 0
		for _, arg := range args[1:] {
			switch arg {
			case "--list", "-l", "--get", "--get-all", "--get-regexp":
				if action != "" {
					return false
				}
				action = arg
			case "--local", "--global", "--system", "--worktree", "--includes", "--no-includes", "--show-origin", "--show-scope", "--name-only", "--null", "-z":
			default:
				if strings.HasPrefix(arg, "-") {
					return false
				}
				operands++
			}
		}
		switch action {
		case "--list", "-l":
			return operands == 0
		case "--get", "--get-all", "--get-regexp":
			return operands == 1 || operands == 2
		}
	}
	return false
}

func isBarePOSIXCommand(name string) bool {
	return name != "" && name != "." && name != ".." &&
		!strings.ContainsAny(name, `/\\`)
}

func hasAnyArg(args []string, unsafe ...string) bool {
	for _, arg := range args {
		for _, candidate := range unsafe {
			if arg == candidate || strings.HasPrefix(arg, candidate+"=") {
				return true
			}
		}
	}
	return false
}

// wordIsReadOnly walks a word's parts. CmdSubst recurses into inner
// statements; ProcSubst is fail-closed; everything else is allowed.
func wordIsReadOnly(word *syntax.Word, policy ReadOnlyCommandPolicy) bool {
	if word == nil {
		return true
	}
	readonly := true
	syntax.Walk(word, func(node syntax.Node) bool {
		if !readonly {
			return false
		}
		switch n := node.(type) {
		case *syntax.ProcSubst:
			readonly = false
			return false
		case *syntax.CmdSubst:
			if !stmtsAreReadOnly(n.Stmts, policy) {
				readonly = false
				return false
			}
			return false
		case *syntax.ParamExp:
			// A runtime operand can change where a read lands, but reads no
			// longer require approval outside the cwd. The command's
			// effect remains read-only; unresolved target metadata is retained
			// separately for presentation and write fail-closed behavior.
			return true
		default:
			return true
		}
	})
	return readonly
}

// wordHasProcSubst reports whether a word contains any ProcSubst node.
func wordHasProcSubst(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	found := false
	syntax.Walk(word, func(node syntax.Node) bool {
		if found {
			return false
		}
		if _, ok := node.(*syntax.ProcSubst); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func stmtsAreReadOnly(stmts []*syntax.Stmt, policy ReadOnlyCommandPolicy) bool {
	for _, stmt := range stmts {
		if ro, _ := stmtIsReadOnly(stmt, policy); !ro {
			return false
		}
	}
	return true
}

// Batched substitution changes operand count: uniq's second operand is an
// output, even though a single {} is read-only. Share this with effect discovery.
func findExecReadOnly(nested []string, batch bool, ev *catalogEvaluation) bool {
	if len(nested) == 0 || batch && !catalogNameOnlyRead(nativeSpace, nested[0]) {
		return false
	}
	return ev.processTokens(nested, nativeSpace).BuiltinRead
}
