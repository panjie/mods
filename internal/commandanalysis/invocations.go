package commandanalysis

import (
	"strings"

	"github.com/panjie/mods/internal/commandtrace"

	"mvdan.cc/sh/v3/syntax"
)

// Invocation is syntax-derived argv. Literal=false preserves the fact that
// evaluating an argument or execution context requires running shell code.
type Invocation struct {
	Program string
	Args    []string
	Literal bool
}

// ShellInvocations enumerates every invocation, including nested invocations.
// It never executes the command. Callers must retain uncertainty about control
// flow/configuration mutation rather than resolving later calls using old state.
func ShellInvocations(command string, posix bool) ([]Invocation, bool) {
	var result []Invocation
	if !posix {
		ir, err := parseWithBridge(command)
		if err != nil || len(ir.ParseErrors) > 0 {
			return nil, false
		}
		for _, inv := range ir.Invocations {
			item := Invocation{Program: trimPowerShellLiteral(inv.Name), Literal: !ir.HasControlFlow && !ir.HasStopParsing && len(ir.AssignmentTargets) == 0}
			for _, arg := range inv.Args {
				single, double := powerShellArgQuoting(arg)
				value := trimPowerShellLiteral(arg)
				if shellPathExpressionUnresolvedQuoted(value, single, double) {
					item.Literal = false
				}
				item.Args = append(item.Args, value)
			}
			result = append(result, item)
		}
		return result, true
	}
	commandtrace.POSIXParse()
	file, err := syntax.NewParser(syntax.Variant(syntax.LangPOSIX)).Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		name, literal := staticShellWord(call.Args[0])
		item := Invocation{Program: name, Literal: literal && len(call.Assigns) == 0}
		for _, arg := range call.Args[1:] {
			value, ok := staticShellWord(arg)
			if !ok {
				item.Literal = false
			}
			item.Args = append(item.Args, value)
		}
		result = append(result, item)
		return true
	})
	return result, true
}
