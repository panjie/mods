package commandanalysis

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Arithmetic reinterprets expanded strings. A read-only producer alone is not
// enough: arbitrary output may contain assignments or variable indirection.
// Only numeric literals, arithmetic operators and narrowly proven numeric
// output can avoid an execution-context gap in a mixed read/write command.
func numericArithmetic(expr syntax.ArithmExpr) bool {
	switch n := expr.(type) {
	case *syntax.BinaryArithm:
		switch n.Op.String() {
		case "+", "-", "*", "/", "%":
			return numericArithmetic(n.X) && numericArithmetic(n.Y)
		}
	case *syntax.UnaryArithm:
		if n.Op.String() == "+" || n.Op.String() == "-" {
			return numericArithmetic(n.X)
		}
	case *syntax.ParenArithm:
		return numericArithmetic(n.X)
	case *syntax.Word:
		if len(n.Parts) != 1 {
			return false
		}
		switch p := n.Parts[0].(type) {
		case *syntax.Lit:
			if p.Value == "" {
				return false
			}
			for _, c := range p.Value {
				if c < '0' || c > '9' {
					return false
				}
			}
			return true
		case *syntax.CmdSubst:
			return numericCountSubstitution(p)
		}
	}
	return false
}

func numericCountSubstitution(sub *syntax.CmdSubst) bool {
	if len(sub.Stmts) != 1 {
		return false
	}
	stmt := sub.Stmts[0]
	if stmt.Background || stmt.Coprocess || stmt.Negated {
		return false
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Assigns) > 0 {
		return false
	}
	var args []string
	for _, word := range call.Args {
		v, ok := boundLoopWord(word, "", "", false)
		if !ok {
			return false
		}
		args = append(args, v)
	}
	if len(args) == 0 {
		return false
	}
	for _, r := range stmt.Redirs {
		// Only a literal stdin source; redirected stdout could have no count.
		if r.Op != syntax.RdrIn || r.N != nil && r.N.Value != "0" || r.Word == nil {
			return false
		}
		if _, ok := boundLoopWord(r.Word, "", "", false); !ok {
			return false
		}
	}
	entry := commandCatalog.lookup(nativeSpace, args[0])
	return entry != nil && entry.NumericOutput != nil && entry.NumericOutput(args[1:])
}

// Without a filename wc emits only the selected count, never a label.
func numericWCArgs(args []string) bool {
	return len(args) == 1 && (args[0] == "-l" || args[0] == "-w" || args[0] == "-c" || args[0] == "-m")
}

// A single grep input emits one count. Unknown flags, multiple input files,
// recursive scans and options introducing filename labels are excluded.
func numericGrepArgs(args []string) bool {
	return (len(args) == 2 || len(args) == 3) && args[0] == "-c" && !strings.HasPrefix(args[1], "-") && (len(args) == 2 || !strings.HasPrefix(args[2], "-"))
}
