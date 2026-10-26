package commandanalysis

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// StructureCode describes syntax or missing invocation evidence. These codes
// deliberately do not share AddGap, which changes write coverage.
type StructureCode string

const (
	StructureControlFlow       StructureCode = "control_flow"
	StructureState             StructureCode = "state_assignment"
	StructureScriptBlock       StructureCode = "script_block"
	StructureHost              StructureCode = "nested_host"
	StructurePayload           StructureCode = "inline_payload"
	StructureUnknownInvocation StructureCode = "unmodeled_invocation"
	StructureMethod            StructureCode = "method_expression"
)

type StructureFinding struct {
	Code       StructureCode
	NodeID     int
	Start, End int  // Half-open offsets; unit is specified by CommandStructure.
	Located    bool // False means command-level; zero is not a fabricated location.
}

// CommandStructure is a bounded explanation of the existing parse and cached
// invocation facts, not proof that a subtree or the whole command is safe.
type CommandStructure struct {
	OffsetUnit                                   string // "byte" for POSIX, "utf16" for PowerShell.
	ControlFlowDepth, ScriptBlockDepth           int
	Assignments, HostInvocations, InlinePayloads int
	ModeledInvocations, UnmodeledInvocations     int
	Findings                                     []StructureFinding
	Truncated                                    bool
}

const maxStructureFindings = 128

func (s *CommandStructure) add(code StructureCode, n parsedNode) {
	if len(s.Findings) == maxStructureFindings {
		s.Truncated = true
		return
	}
	s.Findings = append(s.Findings, StructureFinding{Code: code, NodeID: n.ID, Start: int(n.Start), End: int(n.End), Located: n.ID > 0})
}

// No parsing, rule evaluation, host calls or source execution is permitted here.
// The semantic cache was populated by assessment, even for rejected subtrees.
func commandStructure(p *parsedCommand) *CommandStructure {
	if p == nil || p.err != nil || p.file == nil && p.ir == nil {
		return nil
	}
	s := &CommandStructure{OffsetUnit: "byte"}
	nodes := p.nodes
	if p.ir != nil {
		s.OffsetUnit = "utf16"
		nodes = make([]parsedNode, 0, len(p.ir.Nodes))
		for _, n := range p.ir.Nodes {
			nodes = append(nodes, parsedNode{ID: n.ID, Parent: n.Parent, Start: uint(n.Start), End: uint(n.End), Kind: n.Kind})
		}
	}
	byID := make(map[int]parsedNode, len(nodes))
	type depths struct{ control, block int }
	depth := make(map[int]depths, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
		d := depth[n.Parent]
		switch n.Kind {
		case "*syntax.ForClause", "*syntax.WhileClause", "*syntax.IfClause", "*syntax.CaseClause", "IfStatementAst", "SwitchStatementAst", "ForStatementAst", "ForEachStatementAst", "WhileStatementAst", "DoWhileStatementAst", "DoUntilStatementAst", "TryStatementAst":
			d.control++
			s.add(StructureControlFlow, n)
		case "ScriptBlockExpressionAst", "*syntax.FuncDecl":
			d.block++
			s.add(StructureScriptBlock, n)
		case "AssignmentStatementAst", "*syntax.Assign":
			s.Assignments++
			s.add(StructureState, n)
		case "InvokeMemberExpressionAst":
			s.add(StructureMethod, n)
		}
		depth[n.ID] = d
		s.ControlFlowDepth = max(s.ControlFlowDepth, d.control)
		s.ScriptBlockDepth = max(s.ScriptBlockDepth, d.block)
	}
	invocation := func(id int, name string, args []string) {
		n := byID[id]
		var f *InvocationFacts
		if p.semantics != nil {
			f = p.semantics.byID[id]
		}
		if f != nil && f.Evidence == EvidenceStatic && f.Effect != EffectUnknown {
			s.ModeledInvocations++
		} else {
			s.UnmodeledInvocations++
			s.add(StructureUnknownInvocation, n)
		}
		name = normalizePowerShellCommandName(name)
		if shellHostPrograms[name] {
			s.HostInvocations++
			s.add(StructureHost, n)
			for _, arg := range args {
				switch strings.ToLower(arg) {
				case "-c", "-command", "-encodedcommand", "-enc", "-en", "/c", "/k":
					s.InlinePayloads++
					s.add(StructurePayload, n)
					return
				}
			}
		}
	}
	if p.ir != nil {
		for _, inv := range p.ir.Invocations {
			invocation(inv.NodeID, inv.Name, inv.Args)
		}
		// Old/synthetic IR may carry only command names. Preserve a truthful
		// command-level explanation without inventing source positions.
		if len(p.ir.Invocations) == 0 {
			for _, name := range p.ir.Commands {
				invocation(0, name, nil)
			}
		}
	} else {
		syntax.Walk(p.file, func(n syntax.Node) bool {
			call, ok := n.(*syntax.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			args := shellWordsForAccess(call.Args)
			name, _ := staticShellWord(call.Args[0])
			if len(args) > 0 {
				args = args[1:]
			}
			invocation(p.nodeIDs[call], name, args)
			return true
		})
	}
	return s
}
