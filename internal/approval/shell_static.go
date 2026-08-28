package approval

import (
	"strings"
)

type ShellStaticClass string

const (
	ShellStaticUnknown ShellStaticClass = "unknown"
	ShellStaticRead    ShellStaticClass = "read"
	ShellStaticWrite   ShellStaticClass = "write"
)

type ShellStaticAnalysis struct {
	Class           ShellStaticClass
	AffectedDirs    []string
	UnresolvedPaths []string
	Reason          string
}

// AnalyzeShellStatic performs deterministic shell access classification.
// It returns unknown when the command cannot be proven read-only or tied to
// concrete write targets; callers can then fall back to slower classifiers.
func AnalyzeShellStatic(command string, posix bool) ShellStaticAnalysis {
	return AnalyzeShellStaticWithPolicy(command, posix, ReadOnlyCommandPolicy{})
}

// AnalyzeShellStaticWithPolicy performs deterministic shell access
// classification with user-configured read-only command names.
func AnalyzeShellStaticWithPolicy(command string, posix bool, policy ReadOnlyCommandPolicy) ShellStaticAnalysis {
	assessment := AssessShellStaticWithPolicy(command, posix, policy)
	result := ShellStaticAnalysis{
		AffectedDirs:    append([]string(nil), assessment.KnownDirs...),
		UnresolvedPaths: append([]string(nil), assessment.DynamicTargets...),
		Reason:          assessment.Reason,
	}
	switch assessment.Effect {
	case EffectRead:
		result.Class = ShellStaticRead
		// The legacy static result historically exposed only write targets.
		result.AffectedDirs = nil
	case EffectWrite:
		result.Class = ShellStaticWrite
	default:
		result.Class = ShellStaticUnknown
		result.Reason = ""
	}
	return result
}

// AnalyzeArgvStaticWithPolicy classifies a direct executable invocation whose
// arguments are literal values rather than shell source. Executable paths are
// intentionally not trusted by basename: a workspace binary named like a
// read-only system command may have arbitrary behavior.
func AnalyzeArgvStaticWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) ShellStaticAnalysis {
	assessment := AssessArgvStaticWithPolicy(program, args, posix, policy)
	result := ShellStaticAnalysis{
		AffectedDirs: append([]string(nil), assessment.KnownDirs...),
		Reason:       assessment.Reason,
	}
	switch assessment.Effect {
	case EffectRead:
		result.Class = ShellStaticRead
	case EffectWrite:
		result.Class = ShellStaticWrite
	default:
		result.Class = ShellStaticUnknown
		result.Reason = ""
	}
	return result
}

// AssessArgvStaticWithPolicy assesses a direct executable invocation. Every
// argument is treated as a literal value; no shell expansion is available.
func AssessArgvStaticWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	program = strings.TrimSpace(program)
	result := UnknownCommandAssessment()
	result.Shape = CommandShape{TopLevelActions: 1, Pipelines: 1}
	result.Reviewability = AnalyzeProcessReviewability(program, args, posix)
	result.Shape.Opaque = result.Reviewability.Level == ReviewabilityOpaque
	if program == "" || strings.ContainsAny(program, `/\`) {
		return result
	}
	tokens := append([]string{program}, args...)
	if !posix {
		if policy.matchesPowerShell(program) {
			result.Effect = EffectRead
			result.Reason = policy.reason(program)
			return result
		}
		tokens[0] = normalizePowerShellCommandName(program)
		policy = ReadOnlyCommandPolicy{}
	}
	if ro, reason := invocationTokensReadOnly(tokens, policy); ro {
		result.Effect = EffectRead
		result.Reason = reason
		return result
	}
	dirs := analyzeLiteralWritableTargetsFromTokens(tokens, posix).Dirs
	if len(dirs) == 0 && !hasKnownRiskyInvocation(tokens, posix) {
		return result
	}
	result.Effect = EffectWrite
	result.KnownDirs = dirs
	result.Reason = "write command (static argv analysis)"
	return result
}

func analyzePowerShellWritablePathsIR(ir *psBridgeIR, policy ReadOnlyCommandPolicy) (dirs, unresolved []string, known bool) {
	if ir == nil || len(ir.ParseErrors) > 0 {
		return nil, nil, false
	}
	for _, inv := range ir.Invocations {
		tokens := append([]string{inv.Name}, inv.Args...)
		analysis := analyzeWritableTargetsFromTokens(tokens, false)
		if !analysis.Known {
			args := inv.Args
			if readOnlyPowerShellInvocation(inv, policy) {
				args = powerShellReadPathArguments(inv)
			}
			for _, arg := range args {
				trimmed := strings.TrimSpace(arg)
				single, double := powerShellArgQuoting(trimmed)
				value := trimPowerShellLiteral(trimmed)
				if shellPathExpressionUnresolvedQuoted(value, single, double) {
					unresolved = append(unresolved, value)
				}
			}
			continue
		}
		known = true
		dirs = append(dirs, analysis.Dirs...)
		unresolved = append(unresolved, analysis.Unresolved...)
	}
	unresolved = append(unresolved, safePowerShellDynamicTargets(ir)...)
	return dedupeSorted(dirs), dedupeSorted(unresolved), known
}

func safePowerShellDynamicTargets(ir *psBridgeIR) []string {
	if ir == nil {
		return nil
	}
	var targets []string
	profileMember := false
	for _, expression := range ir.MemberExpressions {
		if profileValueExpression.MatchString(strings.TrimSpace(expression)) {
			profileMember = true
			targets = append(targets, strings.TrimSpace(expression))
		}
	}
	for _, variable := range ir.Variables {
		name := normalizePowerShellVariableName(variable)
		switch {
		case name == "profile" && !profileMember:
			targets = append(targets, "$PROFILE")
		case strings.HasPrefix(name, "env:") && simplePowerShellLocalVar.MatchString(strings.TrimPrefix(name, "env:")):
			targets = append(targets, "$"+variable)
		}
	}
	for _, expression := range ir.TopLevelValueExpressions {
		if safePowerShellDynamicValueExpression(expression) {
			targets = append(targets, strings.TrimSpace(expression))
		}
	}
	return dedupeSorted(targets)
}

func powerShellReadPathArguments(inv psCommandInvocation) []string {
	name := normalizePowerShellCommandName(inv.Name)
	switch name {
	case "get-childitem", "gci", "ls", "dir",
		"get-content", "gc", "cat", "type",
		"get-item", "gi", "get-itemproperty", "get-itempropertyvalue",
		"test-path", "resolve-path", "get-filehash", "get-acl",
		"convert-path", "split-path", "join-path":
		if paths := powerShellParamValues(append([]string{inv.Name}, inv.Args...), "path", "literalpath"); len(paths) > 0 {
			return paths
		}
		operands := commandOperands(inv.Args)
		if len(operands) > 0 {
			return operands[:1]
		}
	case "select-string":
		return powerShellParamValues(append([]string{inv.Name}, inv.Args...), "path", "literalpath")
	}
	return nil
}
