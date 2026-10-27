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
	LocalStateTargets                            int  `json:",omitempty"`
	DynamicEvaluations                           int  `json:",omitempty"`
	FilePayload                                  bool `json:",omitempty"`
	InlineHostDepth                              int  `json:",omitempty"`
	UnmodeledControlFlowDepth                    int  `json:",omitempty"`
	// Literal source fragments only exclude user-supplied payloads in the app.
	// Never log or serialize them as authorization or provenance claims.
	PayloadSources []string `json:"-"`
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
	locals := map[string]bool{}
	local := func(name string) {
		if name == "" {
			return
		}
		for _, c := range name {
			if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') {
				return
			}
		}
		locals[name] = true
	}
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
	inlineHosts := map[int]bool{}
	payloadSource := func(value string) {
		if value != "" && len(s.PayloadSources) < maxStructureFindings {
			s.PayloadSources = append(s.PayloadSources, value)
		}
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
			s.UnmodeledControlFlowDepth = max(s.UnmodeledControlFlowDepth, depth[id].control)
			s.add(StructureUnknownInvocation, n)
		}
		invokedName := strings.ToLower(name)
		name = normalizePowerShellCommandName(name)
		if invokedName == "eval" || invokedName == "invoke-expression" || invokedName == "iex" {
			s.DynamicEvaluations++
			for _, arg := range args {
				payloadSource(trimPowerShellLiteral(arg))
			}
		}
		inline := false
		for _, arg := range args {
			arg = strings.ToLower(arg)
			if (name == "pwsh" || name == "powershell") && (arg == "-noprofile" || arg == "-noninteractive" || arg == "-nologo") {
				continue
			}
			inline = inlinePayloadFlags[arg] || strings.HasPrefix(arg, "--eval=")
			break // Flags after the script operand are script arguments.
		}
		// Any file-host form we cannot positively identify as inline is excluded
		// from automatic reorganization, including extensionless/dynamic paths.
		if hasScriptFileSuffix(programBaseName(name)) || name == "." || name == "source" || payloadHostProgram(name) && !inline {
			s.FilePayload = true
		}
		if name == "env" || name == "sudo" || name == "command" || name == "nohup" || name == "xargs" {
			// The wrapper's file-vs-inline provenance is not bound by these facts.
			s.FilePayload = true
		}
		if shellHostPrograms[name] {
			s.HostInvocations++
			s.add(StructureHost, n)
			for i, arg := range args {
				switch strings.ToLower(arg) {
				case "-c", "-command", "-encodedcommand", "-enc", "-en", "/c", "/k":
					s.InlinePayloads++
					if i+1 < len(args) {
						payloadSource(trimPowerShellLiteral(args[i+1]))
					}
					inlineHosts[id] = true
					s.add(StructurePayload, n)
					return
				}
			}
		}
	}
	if p.ir != nil {
		for _, name := range p.ir.AssignmentTargets {
			local(normalizePowerShellVariableName(name))
		}
		for _, name := range p.ir.ScriptBlockAssignmentTargets {
			local(normalizePowerShellVariableName(name))
		}
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
			if !ok {
				return true
			}
			if len(call.Args) == 0 {
				for _, assignment := range call.Assigns {
					if assignment.Name != nil {
						local(assignment.Name.Value)
					}
				}
				return true
			}
			// Keep literal flags even when a separate argument is dynamic. These
			// are syntax observations only, never reconstructed executable argv.
			args := make([]string, len(call.Args)-1)
			for i, word := range call.Args[1:] {
				args[i], _ = staticShellWord(word)
			}
			name, _ := staticShellWord(call.Args[0])
			invocation(p.nodeIDs[call], name, args)
			return true
		})
	}
	s.LocalStateTargets = len(locals)
	for id := range inlineHosts {
		count := 0
		for parent := id; parent > 0; parent = byID[parent].Parent {
			if inlineHosts[parent] {
				count++
			}
		}
		s.InlineHostDepth = max(s.InlineHostDepth, count)
	}
	return s
}
