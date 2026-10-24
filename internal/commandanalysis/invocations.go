package commandanalysis

import (
	"mvdan.cc/sh/v3/syntax"
)

// Invocation is syntax-derived argv. Literal=false preserves the fact that
// evaluating an argument or execution context requires running shell code.
type Invocation struct {
	NodeID  int
	Program string
	Args    []string
	Literal bool
}

// ShellInvocations enumerates every invocation, including nested invocations.
// It never executes the command. Callers must retain uncertainty about control
// flow/configuration mutation rather than resolving later calls using old state.
func ShellInvocations(command string, posix bool) ([]Invocation, bool) {
	p := parseCommandLegacy(command, posix)
	return p.invocations()
}

func (p *parsedCommand) invocations() ([]Invocation, bool) {
	if p.err != nil {
		return nil, false
	}
	var result []Invocation
	if p.ir != nil {
		ir := p.ir
		for _, inv := range ir.Invocations {
			item := Invocation{NodeID: inv.NodeID, Program: trimPowerShellLiteral(inv.Name), Literal: !ir.HasControlFlow && !ir.HasStopParsing && len(ir.AssignmentTargets) == 0}
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
	file := p.file
	if file == nil {
		return nil, false
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		name, literal := staticShellWord(call.Args[0])
		item := Invocation{NodeID: p.nodeIDs[call], Program: name, Literal: literal && len(call.Assigns) == 0}
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
