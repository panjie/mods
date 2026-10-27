package commandanalysis

import (
	"slices"
	"strings"
	"unicode/utf16"
)

// Only numeric/text Measure-Object parameter sets with a single static property
// name are modeled here. The existing invocation read rule is unchanged; this
// stronger output-type fact is used solely by the new scalar projection proof.
func measureOutputProperties(args []string) []string {
	seen := map[string]bool{}
	text, numeric := false, false
	properties := []string{"count"}
	for i := 0; i < len(args); i++ {
		parameter := strings.ToLower(args[i])
		if seen[parameter] {
			return nil
		}
		seen[parameter] = true
		switch parameter {
		case "-property":
			i++
			if i == len(args) || !powerShellScalarLiteral(args[i]) || !simplePowerShellLocalVar.MatchString(trimPowerShellLiteral(args[i])) {
				return nil
			}
		case "-sum", "-average", "-minimum", "-maximum":
			numeric = true
			properties = append(properties, parameter[1:])
		case "-line", "-word", "-character":
			if !text {
				properties = nil
			}
			text = true
			properties = append(properties, parameter[1:]+"s")
		default:
			return nil
		}
	}
	if text && numeric {
		return nil
	}
	return properties
}

// Prove only (audited pipeline ending in Measure-Object).KnownScalarField.
// The caller still checks all invocations, redirections, variables, methods,
// assignments and control flow. This function does not evaluate/reparse source
// or establish permission for an arbitrary object property or nested host.
func safeTopLevelPowerShellValues(source string, ir *psBridgeIR, session *semanticSession) bool {
	if safeTopLevelPowerShellValueExpressions(ir.TopLevelValueExpressions) {
		return true
	}
	if ir.Version != "2" || session == nil {
		return false
	}
	units := utf16.Encode([]rune(source))
	byID := make(map[int]psSyntaxNode, len(ir.Nodes))
	children := make(map[int][]psSyntaxNode, len(ir.Nodes))
	for _, n := range ir.Nodes {
		byID[n.ID] = n
		children[n.Parent] = append(children[n.Parent], n)
	}
	text := func(n psSyntaxNode) string {
		if n.Start < 0 || n.End <= n.Start || n.End > len(units) {
			return ""
		}
		return string(utf16.Decode(units[n.Start:n.End]))
	}
	proven := map[string]bool{}
	for _, member := range ir.Nodes {
		if member.Kind != "MemberExpressionAst" {
			continue
		}
		// Require the exact top-level expression ownership chain. A matching
		// nested expression cannot vouch for a different enclosing expression.
		expression := byID[member.Parent]
		outer := byID[expression.Parent]
		block := byID[outer.Parent]
		root := byID[block.Parent]
		if expression.Kind != "CommandExpressionAst" || outer.Kind != "PipelineAst" || block.Kind != "NamedBlockAst" || root.Kind != "ScriptBlockAst" || root.Parent != 0 || len(children[outer.ID]) != 1 {
			continue
		}
		parts := children[member.ID]
		if len(parts) != 2 || parts[0].Kind != "ParenExpressionAst" || parts[1].Kind != "StringConstantExpressionAst" {
			continue
		}
		property := strings.ToLower(text(parts[1]))
		inner := children[parts[0].ID]
		if len(inner) != 1 || inner[0].Kind != "PipelineAst" {
			continue
		}
		commands := children[inner[0].ID]
		if len(commands) == 0 {
			continue
		}
		plain := true
		for _, command := range commands {
			if command.Kind != "CommandAst" {
				plain = false
				break
			}
		}
		if !plain {
			continue
		}
		// Consume only the already cached, built-in semantic fact. Explicit
		// trust and old IR without node identity cannot manufacture this proof.
		fact := session.byID[commands[len(commands)-1].ID]
		if fact == nil || !fact.BuiltinRead || !slices.Contains(fact.ReadProperties, property) {
			continue
		}
		if value := text(member); value != "" {
			proven[value] = true
		}
	}
	for _, expression := range ir.TopLevelValueExpressions {
		if !safeTopLevelPowerShellValueExpressions([]string{expression}) && !proven[strings.TrimSpace(expression)] {
			return false
		}
	}
	return true
}
