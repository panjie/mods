package commandanalysis

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Coverage is proved separately from collecting paths. In particular, a known
// redirection or one known writer cannot hide an unknown invocation beside it.
func assessPOSIXCoverage(result *CommandAssessment, file *syntax.File, policy ReadOnlyCommandPolicy) {
	result.WriteCoverage = CoverageComplete
	if policy.session == nil {
		policy.session = newSemanticSession(&parsedCommand{file: file}, policy)
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 || len(n.Assigns) > 0 {
				result.AddGap(GapExecutionContext)
				return true
			}
			tokens := coverageWords(n.Args)
			f := policy.session.posix(n)
			appendWriteOperands(result, f.CoverageWrites)
			name, _ := staticShellWord(n.Args[0])
			if name == "cd" {
				result.AddGap(GapPathContext)
			}
			// A read does not need concrete argv to prove write coverage.
			// Keep walking its children so substitutions and redirects still
			// contribute their own effects and context gaps.
			if ro, _ := callIsReadOnly(n, policy); ro {
				return true
			}
			if len(tokens) == 0 || !f.BoundedWrite {
				result.AddGap(GapUnknownInvocation)
			}
		case *syntax.ParamExp:
			if n.Exp != nil && (n.Exp.Op == syntax.AssignUnset || n.Exp.Op == syntax.AssignUnsetOrNull) {
				result.AddGap(GapExecutionContext)
			}
		case *syntax.ArithmExp:
			if !numericArithmetic(n.X) {
				result.AddGap(GapExecutionContext)
			}
		case *syntax.ForClause:
			if policy.session.boundedLoop(n).read {
				return false
			}
			result.AddGap(GapControlFlow)
		case *syntax.IfClause, *syntax.WhileClause, *syntax.CaseClause, *syntax.FuncDecl, *syntax.ArithmCmd:
			result.AddGap(GapControlFlow)
		case *syntax.Redirect:
			if redirectionWritesPersistent(n) {
				words := coverageWords([]*syntax.Word{n.Word})
				if len(words) != 1 || words[0] == "" {
					result.AddGap(GapWriteTargets)
				} else {
					result.WritePaths = append(result.WritePaths, words[0])
				}
			}
		}
		return true
	})
	if result.Effect == EffectUnknown {
		result.AddGap(GapUnknownInvocation)
	}
}

// Only quoted, simple environment references retain one operand at runtime.
// Substitutions and unquoted expansions cannot bind a concrete leaf target.
func coverageWords(words []*syntax.Word) []string {
	var args []string
	var appendPart func(*strings.Builder, syntax.WordPart, bool) bool
	appendPart = func(b *strings.Builder, part syntax.WordPart, quoted bool) bool {
		switch p := part.(type) {
		case *syntax.Lit:
			b.WriteString(p.Value)
		case *syntax.SglQuoted:
			if strings.ContainsAny(p.Value, "$`") {
				return false
			}
			b.WriteString(p.Value)
		case *syntax.DblQuoted:
			for _, child := range p.Parts {
				if !appendPart(b, child, true) {
					return false
				}
			}
		case *syntax.ParamExp:
			if !quoted || p.Param == nil || p.Flags != nil || p.Excl || p.Length || p.Width || p.IsSet || p.NestedParam != nil || p.Index != nil || len(p.Modifiers) > 0 || p.Slice != nil || p.Repl != nil || p.Names != 0 || p.Exp != nil {
				return false
			}
			b.WriteString("${" + p.Param.Value + "}")
		default:
			return false
		}
		return true
	}
	for _, word := range words {
		if word == nil {
			return nil
		}
		var b strings.Builder
		for _, part := range word.Parts {
			if !appendPart(&b, part, false) {
				return nil
			}
		}
		args = append(args, b.String())
	}
	return args
}

func assessPowerShellCoverage(result *CommandAssessment, ir *psBridgeIR, policy ReadOnlyCommandPolicy) {
	result.WriteCoverage = CoverageComplete
	if result.Effect == EffectRead {
		return
	}
	if ir.HasControlFlow || ir.HasBackground || ir.HasStopParsing || len(ir.MethodInvocations) > 0 || len(ir.StaticMembers) > 0 {
		result.AddGap(GapControlFlow)
	}
	if powerShellMutatesPathContext(ir) {
		result.AddGap(GapPathContext)
	}
	if policy.session == nil {
		policy.session = newSemanticSession(nil, policy)
	}
	for _, inv := range ir.Invocations {
		if readOnlyPowerShellInvocation(inv, policy) {
			continue
		}
		f := policy.session.ps(inv)
		appendWriteOperands(result, f.CoverageWrites)
		if !f.BoundedWrite {
			result.AddGap(GapUnknownInvocation)
		}
	}
	if _, ok := powerShellRedirectionTargets(ir); !ok {
		result.AddGap(GapWriteTargets)
	}

	if result.Effect == EffectUnknown {
		result.AddGap(GapUnknownInvocation)
	}
}

func collectWriteOperands(result *CommandAssessment, tokens []string, posix bool) {
	f := catalogTokens(tokens, spaceForShell(posix), ReadOnlyCommandPolicy{})
	appendWriteOperands(result, f.CoverageWrites)
}

func boundedWriteInvocation(tokens []string, posix bool) bool {
	return catalogTokens(tokens, spaceForShell(posix), ReadOnlyCommandPolicy{}).BoundedWrite
}
func textOutputOperands(name string, args []string) ([]string, bool, bool) {
	e := commandCatalog.lookup(nativeSpace, name)
	if e == nil || !e.TextOutput {
		return nil, false, false
	}
	b, ok := bindOptions(e.Options, args)
	return b.Operands, b.Flags["reverse"], ok && len(b.Operands) <= 2
}
