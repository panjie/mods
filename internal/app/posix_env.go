package app

import (
	"strings"

	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
)

// posixBindingCommandNames lists shell builtins and utilities that can bind
// or remove shell variables at runtime; any of them suppresses static env
// expansion because the child shell would observe different values. Under
// the POSIX grammar mvdan parses declare/export/local/readonly/typeset as
// plain command invocations, so they are matched by name here.
var posixBindingCommandNames = map[string]bool{
	".": true, "declare": true, "env": true, "eval": true, "export": true,
	"local": true, "mapfile": true, "read": true, "readarray": true,
	"readonly": true, "set": true, "shift": true, "source": true,
	"typeset": true, "unset": true,
}

// commandMutatesPOSIXEnvironment reports whether the command binds, exports,
// or removes shell variables. POSIX keeps one namespace for shell and
// environment variables, so any assignment (including command-local and
// export forms), loop binding, function scope, or read-style builtin makes
// statically known values unreliable for the child shell.
func commandMutatesPOSIXEnvironment(command string) bool {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(command), "")
	if err != nil {
		return true // fail closed on unparseable input
	}
	binding := false
	syntax.Walk(file, func(node syntax.Node) bool {
		if binding {
			return false
		}
		switch node := node.(type) {
		case *syntax.Assign, *syntax.ForClause, *syntax.FuncDecl, *syntax.LetClause, *syntax.DeclClause:
			binding = true
			return false
		case *syntax.CallExpr:
			if len(node.Args) > 0 {
				if name := basePOSIXCommandName(node.Args[0].Lit()); name != "" && posixBindingCommandNames[name] {
					binding = true
					return false
				}
			}
		}
		return true
	})
	return binding
}

func basePOSIXCommandName(literal string) string {
	literal = strings.TrimSpace(literal)
	if idx := strings.LastIndexByte(literal, '/'); idx >= 0 {
		literal = literal[idx+1:]
	}
	return literal
}

// resolvePOSIXEnvTargets materializes dynamic targets referencing
// environment variables whose inherited values are statically known and
// identical inside the child shell; see resolvePowerShellEnvTargets for the
// shared policy. POSIX dynamic targets are bare $NAME references, so only
// the bare-reference decisions apply here.
func resolvePOSIXEnvTargets(known, dynamic []string, workspace, command string, shadowedEnv map[string]bool, allowValueDirs bool) ([]string, []string) {
	if len(dynamic) == 0 {
		return known, dynamic
	}
	opts := pathutil.DefaultOptions(workspace, pathutil.FlavorPOSIX)
	kept := make([]string, 0, len(dynamic))
	var expanded []string
	for _, target := range dynamic {
		trimmed := strings.TrimSpace(target)
		name, pathShaped, ok := pathutil.EnvRefParts(trimmed, pathutil.FlavorPOSIX)
		if !ok || pathShaped || shadowedEnv[name] {
			kept = append(kept, target)
			continue
		}
		if dirs, resolved := bareEnvRefDirs(name, command, opts, allowValueDirs); resolved {
			expanded = appendMissingShellDirs(expanded, dirs)
			continue
		}
		kept = append(kept, target)
	}
	if len(expanded) == 0 && len(kept) == len(dynamic) {
		return known, dynamic
	}
	return appendMissingShellDirs(known, expanded), kept
}
