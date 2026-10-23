package approval

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Coverage is proved separately from collecting paths. In particular, a known
// redirection or one known writer cannot hide an unknown invocation beside it.
func assessPOSIXCoverage(result *CommandAssessment, file *syntax.File, policy ReadOnlyCommandPolicy) {
	result.WriteCoverage = CoverageComplete
	syntax.Walk(file, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			if len(n.Args) == 0 || len(n.Assigns) > 0 {
				result.AddGap(GapExecutionContext)
				return true
			}
			tokens := coverageWords(n.Args)
			collectWriteOperands(result, tokens, true)
			if len(tokens) == 0 {
				result.AddGap(GapUnknownInvocation)
				return true
			}
			if tokens[0] == "cd" {
				result.AddGap(GapPathContext)
			}
			if ro, _ := callIsReadOnly(n, policy); !ro && !boundedWriteInvocation(tokens, true) {
				result.AddGap(GapUnknownInvocation)
			}
		case *syntax.IfClause, *syntax.ForClause, *syntax.WhileClause, *syntax.CaseClause, *syntax.FuncDecl, *syntax.ArithmCmd:
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
	for _, inv := range ir.Invocations {
		if readOnlyPowerShellInvocation(inv, policy) {
			continue
		}
		collectWriteOperands(result, append([]string{inv.Name}, inv.Args...), false)
		if !boundedWriteInvocation(append([]string{inv.Name}, inv.Args...), false) {
			result.AddGap(GapUnknownInvocation)
		}
	}
	// The current IR does not bind a redirection path to its operation. Until
	// that is modeled, other known targets must not make it reusable.
	for _, redir := range ir.Redirects {
		if redir == "FileRedirection" {
			result.AddGap(GapWriteTargets)
		}
	}
	if result.Effect == EffectUnknown {
		result.AddGap(GapUnknownInvocation)
	}
}

func collectWriteOperands(result *CommandAssessment, tokens []string, posix bool) {
	targets, _ := writableTargetsFromTokens(tokens, posix)
	for _, target := range targets {
		value := trimPowerShellLiteral(strings.TrimSpace(target.path))
		if value != "" {
			result.WritePaths = append(result.WritePaths, value)
		}
	}
}

func boundedWriteInvocation(tokens []string, posix bool) bool {
	if len(tokens) == 0 {
		return false
	}
	name := tokens[0]
	if posix && !isBarePOSIXCommand(name) || !posix && !isBarePowerShellCommand(name) {
		return false
	}
	if !posix {
		name = canonicalPowerShellWriteCommand(normalizePowerShellCommandName(name))
	}
	// Keep this set narrow. Collecting a filename from find -exec, git hooks,
	// an interpreter, or an unknown program cannot bound its side effects.
	switch name {
	case "touch", "tee", "mkdir", "rm", "rmdir", "unlink", "chmod", "chown",
		"set-content", "add-content", "clear-content", "out-file", "remove-item":
	case "uniq", "xxd":
		_, _, ok := textOutputOperands(name, tokens[1:])
		if !ok {
			return false
		}
	default:
		return false
	}
	analysis := analyzeWritableTargetsFromTokens(tokens, posix)
	return analysis.Known && (len(analysis.Dirs) > 0 || len(analysis.Unresolved) > 0 || len(analysis.ProviderTargets) > 0)
}

// textOutputOperands implements a deliberately bounded subset of the utilities'
// option grammars. Unknown forms stay reviewable instead of guessing argv roles.
func textOutputOperands(name string, args []string) (operands []string, reverse, ok bool) {
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if !options || arg == "-" || !strings.HasPrefix(arg, "-") {
			operands = append(operands, arg)
			continue
		}
		if name == "uniq" {
			switch arg {
			case "-c", "-d", "-u", "-i", "-z", "--count", "--repeated", "--unique", "--ignore-case", "--zero-terminated":
			case "-f", "-s", "-w", "--skip-fields", "--skip-chars", "--check-chars":
				i++
				if i >= len(args) {
					return nil, false, false
				}
			default:
				return nil, false, false
			}
		} else {
			switch arg {
			case "-r", "-revert":
				reverse = true
			case "-a", "-b", "-e", "-i", "-p", "-ps", "-u":
			case "-c", "-cols", "-g", "-groupsize", "-l", "-len", "-s", "-seek", "-o":
				i++
				if i >= len(args) {
					return nil, false, false
				}
			default:
				return nil, false, false
			}
		}
	}
	return operands, reverse, len(operands) <= 2
}
