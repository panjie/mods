package approval

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Scalar-confined command substitutions. A substitution on this allowlist
// provably emits output containing no path separator and no ".." component,
// so its expansion cannot move a filesystem target outside the static
// directory prefix of the word it appears in. Materializing such words lets
// timestamped or unique-suffix backup commands (for example
// `cp a.conf a.conf.$(date +%s)`) keep a deterministic affected directory
// instead of failing closed as an unresolvable dynamic target.

// scalarConfinedCommands maps an allowlisted program name to its literal
// argument validation. Forms are deliberately narrow: unknown flags,
// file-reading inputs, and dynamic arguments all reject confinement, and any
// program outside the map rejects by default.
var scalarConfinedCommands = map[string]func(args []string) bool{
	"date":     scalarDateArgs,
	"hostname": scalarFlaglessArgs,
	"id":       scalarFlaglessArgs,
	"uuidgen":  scalarFlaglessArgs,
	"whoami":   scalarFlaglessArgs,
}

// scalarFlaglessArgs accepts the bare invocation only; every flag form of
// these programs either reads further inputs or changes behavior the
// allowlist has not proven.
func scalarFlaglessArgs(args []string) bool {
	return len(args) == 0
}

// scalarDateArgs accepts date invocations whose displayed output cannot
// contain "/": every argument must be literal, contain no "/", and not name
// a format-file input (-f/--file) whose lines could embed "/" in the output.
func scalarDateArgs(args []string) bool {
	for _, arg := range args {
		if arg == "" || strings.Contains(arg, "/") {
			return false
		}
		if arg == "-f" || arg == "--file" || strings.HasPrefix(arg, "-f") {
			return false
		}
	}
	return true
}

// cmdSubstScalarConfined reports whether the command substitution provably
// emits output free of path separators and ".." components. It requires a
// single plain invocation with literal arguments; pipelines, redirections,
// assignments, compound statements, and nested expansions all reject.
func cmdSubstScalarConfined(subst *syntax.CmdSubst) bool {
	if subst == nil || len(subst.Stmts) != 1 {
		return false
	}
	stmt := subst.Stmts[0]
	if stmt == nil || stmt.Cmd == nil || stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 || len(call.Assigns) > 0 {
		return false
	}
	argv := make([]string, 0, len(call.Args))
	for _, word := range call.Args {
		value, ok := staticShellWord(word)
		if !ok || value == "" {
			return false
		}
		argv = append(argv, value)
	}
	validate, ok := scalarConfinedCommands[path.Base(argv[0])]
	return ok && validate(argv[1:])
}

// posixStateBindingCommandNames lists commands that can bind or remove shell
// variables, shadow builtins, or otherwise redirect program resolution. Any
// of them disables scalar confinement for the whole command.
var posixStateBindingCommandNames = map[string]bool{
	".": true, "alias": true, "declare": true, "env": true, "eval": true,
	"export": true, "local": true, "readonly": true, "set": true,
	"shift": true, "source": true, "typeset": true, "unset": true,
}

// posixFileAllowsScalarConfinement reports whether the parsed command leaves
// allowlisted programs unshadowed. Assignments, function definitions, and
// state-binding builtins could redefine PATH or hijack a program name before
// the substitution runs, so any of them disables confinement command-wide.
func posixFileAllowsScalarConfinement(file *syntax.File) bool {
	if file == nil {
		return false
	}
	allowed := true
	syntax.Walk(file, func(node syntax.Node) bool {
		if !allowed {
			return false
		}
		switch node := node.(type) {
		case *syntax.Assign, *syntax.FuncDecl, *syntax.DeclClause:
			allowed = false
			return false
		case *syntax.CallExpr:
			if len(node.Args) == 0 {
				return true
			}
			if name, ok := staticShellWord(node.Args[0]); ok && posixStateBindingCommandNames[name] {
				allowed = false
				return false
			}
		}
		return true
	})
	return allowed
}
