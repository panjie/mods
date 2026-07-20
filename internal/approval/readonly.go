package approval

import (
	"path"
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
	command = strings.TrimSpace(command)
	if command == "" {
		return false, ""
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return false, ""
	}
	for _, stmt := range file.Stmts {
		if ro, _ := stmtIsReadOnly(stmt, policy); !ro {
			return false, ""
		}
	}
	return true, "read-only command (AST analysis)"
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
	for _, arg := range call.Args {
		if !wordIsReadOnly(arg, policy) {
			return false, ""
		}
	}
	name, ok := staticShellWord(call.Args[0])
	if !ok || name == "" {
		return false, ""
	}
	name = path.Base(name)
	if policy.matchesPOSIX(name) {
		return true, policy.reason(name)
	}
	if name != "env" && name != "xxd" && readOnlyCommands[name] {
		return true, "read-only command: " + name
	}
	args := shellWordsForAccess(call.Args)
	if len(args) > 0 {
		args[0] = name
	}
	if readOnly, reason := invocationTokensReadOnly(args, policy); readOnly {
		return true, reason
	}
	return false, ""
}

// invocationTokensReadOnly classifies one statically tokenized command. It
// validates arguments as well as the executable name so wrappers and
// output-producing flags cannot inherit a read-only classification merely
// from their first token.
func invocationTokensReadOnly(args []string, policy ReadOnlyCommandPolicy) (bool, string) {
	if len(args) == 0 || args[0] == "" {
		return false, ""
	}
	name := path.Base(args[0])
	if policy.matchesPOSIX(name) {
		return true, policy.reason(name)
	}
	if name == "env" {
		return envInvocationReadOnly(args[1:], policy)
	}
	switch name {
	case "find":
		if findInvocationReadOnly(args[1:]) {
			return true, "read-only command: find"
		}
		return false, ""
	case "sort":
		if sortInvocationReadOnly(args[1:]) {
			return true, "read-only command: sort"
		}
		return false, ""
	case "xargs":
		if xargsInvocationReadOnly(args[1:]) {
			return true, "read-only command: xargs"
		}
		return false, ""
	}
	if readOnlyCommands[name] {
		if name == "xxd" && hasAnyArg(args[1:], "-r", "--revert") {
			return false, ""
		}
		return true, "read-only command: " + name
	}
	if readOnlySubcommandInvocation(name, args[1:]) {
		return true, "read-only subcommand: " + name + " " + strings.ToLower(args[1])
	}
	return false, ""
}

func findInvocationReadOnly(args []string) bool {
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
		default:
			return false
		}
	}
	return true
}

func findExpressionToken(arg string) bool {
	return arg == "!" || arg == "(" || arg == ")" || strings.HasPrefix(arg, "-")
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
	name := path.Base(nested[0])
	if !readOnlyCommands[name] || name == "xxd" {
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

func envInvocationReadOnly(args []string, policy ReadOnlyCommandPolicy) (bool, string) {
	nested, ok := envCommandArgs(args)
	if !ok {
		return false, ""
	}
	if len(nested) == 0 {
		return true, "read-only command: env"
	}
	return invocationTokensReadOnly(nested, policy)
}

func envCommandArgs(args []string) ([]string, bool) {
	for len(args) > 0 {
		arg := args[0]
		switch {
		case arg == "--":
			if len(args) == 1 {
				return nil, false
			}
			return args[1:], true
		case isEnvAssignment(arg):
			args = args[1:]
		case strings.HasPrefix(arg, "-"):
			// Options such as -S/--split-string can hide another command and
			// require option-aware parsing. Treat them as unknown.
			return nil, false
		default:
			return args, true
		}
	}
	return nil, true
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

func readOnlySubcommandInvocation(name string, args []string) bool {
	if len(args) == 0 {
		return false
	}
	subcommands, ok := subcommandReadOnly[name]
	if !ok {
		return false
	}
	subcmd := strings.ToLower(args[0])
	if subcmd == "" || strings.HasPrefix(subcmd, "-") || !subcommands[subcmd] {
		return false
	}
	if name == "git" && hasAnyArg(args[1:], "--output", "--ext-diff", "--textconv") {
		return false
	}
	return true
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
			if _, ok := simpleHomeExpansion(n); !ok {
				// Arbitrary runtime values may resolve outside the workspace.
				readonly = false
				return false
			}
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

// readOnlyCommands are always read-only regardless of flags.
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"wc": true, "file": true, "stat": true, "pwd": true,
	"echo": true, "date": true, "whoami": true, "hostname": true,
	"uname": true, "du": true, "df": true, "which": true,
	"printenv": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true,
	"grep": true, "egrep": true, "fgrep": true,
	"diff": true, "uniq": true, "comm": true, "tr": true,
	"cut": true, "strings": true, "xxd": true, "od": true,
	"hexdump": true, "nm": true, "objdump": true, "readelf": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"shasum": true, "cksum": true, "test": true, "[": true,
	"true": true, "false": true, "seq": true, "printf": true,
	"id": true, "groups": true, "lsof": true, "ps": true,
	"free": true, "uptime": true, "w": true, "column": true,
	"paste": true, "expand": true, "unexpand": true, "nl": true,
	"rev": true, "tac": true, "fold": true, "fmt": true,
	"join": true,
	// Shell builtins that only affect shell state, never the filesystem.
	// Recognising cd as read-only lets the common "cd /path && cmd" pattern
	// pass the static classifier instead of falling through to the LLM.
	"cd": true,
}

// subcommandReadOnly maps a tool to its read-only subcommands.
var subcommandReadOnly = map[string]map[string]bool{
	"git": {
		"status": true, "log": true, "diff": true, "show": true,
		"blame": true, "annotate": true, "rev-parse": true, "describe": true,
		"reflog": true, "shortlog": true, "ls-files": true, "ls-tree": true,
		"ls-remote": true, "whatchanged": true, "cat-file": true,
		"rev-list": true, "merge-base": true, "name-rev": true,
		"var": true, "for-each-ref": true,
	},
	"docker": {
		"ps": true, "images": true, "logs": true, "inspect": true,
		"stats": true, "top": true, "history": true, "search": true,
		"version": true, "info": true, "events": true,
	},
	"kubectl": {
		"get": true, "describe": true, "logs": true, "explain": true,
		"top": true, "version": true, "api-resources": true,
		"api-versions": true, "cluster-info": true, "diff": true,
	},
	"go": {
		"version": true, "list": true, "vet": true, "doc": true,
		"help": true,
	},
	"npm": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
	"pnpm": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
	"yarn": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
}
