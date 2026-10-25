package commandanalysis

import "mvdan.cc/sh/v3/syntax"

func posixAnalysisDiagnostics(file *syntax.File, session *semanticSession) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			out = append(out, s)
			seen[s] = true
		}
	}
	syntax.Walk(file, func(n syntax.Node) bool {
		switch n := n.(type) {
		case *syntax.ForClause:
			if session.boundedLoop(n).read {
				return false
			}
			if it, ok := n.Loop.(*syntax.WordIter); ok {
				add("Loop " + it.Name.Value + ": " + session.boundedLoop(n).reason)
			} else {
				add("Unmodeled loop")
			}
		case *syntax.ParamExp:
			if n.Exp != nil && (n.Exp.Op == syntax.AssignUnset || n.Exp.Op == syntax.AssignUnsetOrNull) {
				add("Parameter expansion may assign an execution-context variable")
			}
		case *syntax.ArithmExp:
			if !numericArithmetic(n.X) {
				add("Arithmetic expansion is not proven numeric and context-preserving")
			}
		case *syntax.CallExpr:
			f := session.posix(n)
			if len(n.Args) > 0 {
				if name, ok := staticShellWord(n.Args[0]); ok && name == "cd" {
					add("Working directory changes inside the command; use the tool cwd argument for a fixed directory")
				}
			}
			policy := session.policy
			policy.session = session
			ro, _ := callIsReadOnly(n, policy)
			if !ro && !f.KnownWrite && len(n.Args) > 0 {
				if name, ok := staticShellWord(n.Args[0]); ok {
					add("Unmodeled command or arguments: " + name)
				} else {
					add("Unresolved command name")
				}
			}
		}
		return true
	})
	return out
}
