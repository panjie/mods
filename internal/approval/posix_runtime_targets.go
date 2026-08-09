package approval

import "mvdan.cc/sh/v3/syntax"

// unresolvedPOSIXRuntimeExpressionsFromFile reports runtime expressions unless
// their local AST position proves that they are scalar data rather than a
// filesystem target. This is intentionally conservative: it does not try to
// infer the output provenance of commands or pipelines.
func unresolvedPOSIXRuntimeExpressionsFromFile(file *syntax.File) []string {
	if file == nil {
		return nil
	}

	scalar := posixScalarRuntimeExpressions(file)
	var unresolved []string
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			if _, known := simpleHomeExpansion(node); known {
				return true
			}
			if _, ok := scalar[node]; ok {
				return true
			}
			if node.Param != nil && node.Param.Value != "" {
				unresolved = append(unresolved, "$"+node.Param.Value)
			} else {
				unresolved = append(unresolved, "parameter expansion")
			}
		case *syntax.CmdSubst:
			if _, ok := scalar[node]; !ok {
				unresolved = append(unresolved, "command substitution")
			}
		case *syntax.ArithmExp:
			if _, ok := scalar[node]; !ok {
				unresolved = append(unresolved, "arithmetic expansion")
			}
		case *syntax.ProcSubst:
			if _, ok := scalar[node]; !ok {
				unresolved = append(unresolved, "process substitution")
			}
		}
		// Even when the outer expression is scalar, inspect commands nested in
		// command/process substitutions for their own runtime path operands.
		return true
	})
	return dedupeSorted(unresolved)
}

// posixScalarRuntimeExpressions marks only syntax positions whose values are
// consumed as data. It deliberately contains no command-output allowlist.
func posixScalarRuntimeExpressions(file *syntax.File) map[syntax.Node]struct{} {
	scalar := make(map[syntax.Node]struct{})
	markWord := func(word *syntax.Word) {
		markPOSIXScalarWord(word, scalar)
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Assign:
			markWord(node.Value)
		case *syntax.WordIter:
			for _, word := range node.Items {
				markWord(word)
			}
		case *syntax.CallExpr:
			markPOSIXScalarCallArgs(node, markWord)
		}
		return true
	})
	return scalar
}

func markPOSIXScalarWord(word *syntax.Word, scalar map[syntax.Node]struct{}) {
	if word == nil {
		return
	}
	syntax.Walk(word, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			scalar[node] = struct{}{}
			return true
		case *syntax.CmdSubst:
			scalar[node] = struct{}{}
			return false
		case *syntax.ArithmExp:
			scalar[node] = struct{}{}
			return false
		case *syntax.ProcSubst:
			scalar[node] = struct{}{}
			return false
		default:
			return true
		}
	})
}

func markPOSIXScalarCallArgs(call *syntax.CallExpr, markWord func(*syntax.Word)) {
	if call == nil || len(call.Args) == 0 {
		return
	}
	name, ok := staticShellWord(call.Args[0])
	if !ok {
		return
	}

	switch name {
	case "echo", "printf":
		for _, word := range call.Args[1:] {
			markWord(word)
		}
	case "find":
		markPOSIXFindScalarArgs(call.Args[1:], markWord)
	case "test", "[":
		markPOSIXTestScalarArgs(call.Args[1:], markWord)
	}
}

func markPOSIXFindScalarArgs(args []*syntax.Word, markWord func(*syntax.Word)) {
	for i := 0; i+1 < len(args); i++ {
		option, ok := staticShellWord(args[i])
		if !ok || !posixFindScalarOption(option) {
			continue
		}
		markWord(args[i+1])
		i++
	}
}

func posixFindScalarOption(option string) bool {
	switch option {
	case "-name", "-iname", "-path", "-ipath", "-regex", "-iregex",
		"-type", "-xtype", "-size", "-links", "-inum", "-perm",
		"-user", "-group", "-uid", "-gid", "-fstype", "-maxdepth",
		"-mindepth", "-atime", "-amin", "-ctime", "-cmin", "-mtime", "-mmin":
		return true
	default:
		return false
	}
}

func markPOSIXTestScalarArgs(args []*syntax.Word, markWord func(*syntax.Word)) {
	for i, word := range args {
		operator, ok := staticShellWord(word)
		if !ok {
			continue
		}
		if (operator == "-n" || operator == "-z") && i+1 < len(args) {
			markWord(args[i+1])
			continue
		}
		if !posixScalarComparison(operator) {
			continue
		}
		if i > 0 {
			markWord(args[i-1])
		}
		if i+1 < len(args) {
			markWord(args[i+1])
		}
	}
}

func posixScalarComparison(operator string) bool {
	switch operator {
	case "=", "==", "!=", "<", ">", "-eq", "-ne", "-gt", "-ge", "-lt", "-le":
		return true
	default:
		return false
	}
}
