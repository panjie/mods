package commandanalysis

import (
	"strings"
)

// AssessArgvStaticWithPolicy assesses a direct executable invocation. Every
// argument is treated as a literal value; no shell expansion is available.
func AssessArgvStaticWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	return AssessArgvStaticWithContext(program, args, posix, policy, ArgvStaticContext{})
}

// AssessArgvStaticWithContext assesses a direct executable invocation with
// deterministic execution context used by program-specific analyzers.
func AssessArgvStaticWithContext(program string, args []string, posix bool, policy ReadOnlyCommandPolicy, context ArgvStaticContext) (result CommandAssessment) {
	defer func() {
		result.StaticRead = result.Effect == EffectRead
		if result.Effect != EffectUnknown {
			result.Evidence = EvidenceStatic
		}
		if strings.HasPrefix(result.Reason, "user-configured") {
			result.Evidence = EvidenceTrust
		}
		if result.Effect == EffectRead {
			result.WriteCoverage = CoverageComplete
		}
	}()
	program = strings.TrimSpace(program)
	result = UnknownCommandAssessment()
	result.Shape = CommandShape{TopLevelActions: 1, Pipelines: 1}
	result.Reviewability = AnalyzeProcessReviewabilityWithPolicy(program, args, posix, policy)
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
	facts := catalogProcessTokens(tokens, policy)
	if facts.ReadOnly {
		result.Effect = EffectRead
		result.Reason = facts.ReadReason
		return result
	}
	if gitResult, handled := assessGitArgvStatic(tokens, posix, context); handled {
		gitResult.Shape = result.Shape
		gitResult.Reviewability = result.Reviewability
		return gitResult
	}
	analysis := analyzeWritableTargets(facts.Writes, facts.KnownWrite, posix, false)
	if len(analysis.Dirs) == 0 && len(analysis.ProviderTargets) == 0 && !facts.Risky {
		return result
	}
	result.Effect = EffectWrite
	appendWriteOperands(&result, facts.CoverageWrites)
	result.KnownDirs = analysis.Dirs
	result.ProviderWriteTargets = analysis.ProviderTargets
	result.Reason = "write command (static argv analysis)"
	if facts.BoundedWrite {
		result.WriteCoverage = CoverageComplete
	} else {
		result.AddGap(GapWriteTargets)
	}
	return result
}

func analyzePowerShellWritablePathsIR(ir *psBridgeIR, policy ReadOnlyCommandPolicy, cwd string) (dirs, unresolved, providerTargets []string, known bool) {
	return analyzePowerShellWritablePathsContext(ir, policy, ArgvStaticContext{Cwd: cwd})
}
func analyzePowerShellWritablePathsContext(ir *psBridgeIR, policy ReadOnlyCommandPolicy, context ArgvStaticContext) (dirs, unresolved, providerTargets []string, known bool) {
	if ir == nil || len(ir.ParseErrors) > 0 {
		return nil, nil, nil, false
	}
	if policy.session == nil {
		policy.session = newSemanticSession(nil, policy)
	}
	context.semantics = policy.session
	var readUnresolved []string
	for _, inv := range ir.Invocations {
		tokens := append([]string{inv.Name}, inv.Args...)
		f := policy.session.ps(inv)
		if gitResult := policy.session.git(f, tokens, false, context); gitResult != nil {
			known = true
			dirs = append(dirs, gitResult.KnownDirs...)
			continue
		}
		analysis := analyzeWritableTargets(f.Writes, f.KnownWrite, false, true, context.pathOptions(false))
		if !analysis.Known {
			args := inv.Args
			readOnly := readOnlyPowerShellInvocation(inv, policy)
			if readOnly {
				args = powerShellReadPathArguments(inv)
			}
			for _, arg := range args {
				trimmed := strings.TrimSpace(arg)
				if strings.HasPrefix(trimmed, "{") {
					// A script block argument is code, not a path expression.
					// Its inner invocations are separate IR entries, so real
					// runtime targets inside it are still surfaced.
					continue
				}
				if isPowerShellAutomaticConstantExpression(trimmed) {
					// $true / $false / $null evaluate to a boolean or null,
					// never a path. Without this the encoding constructor in
					// New-Object System.Text.UTF8Encoding($false) shows up as a
					// resolved-attempt runtime target such as "($false)".
					continue
				}
				single, double := powerShellArgQuoting(trimmed)
				value := trimPowerShellLiteral(trimmed)
				if shellPathExpressionUnresolvedQuoted(value, single, double) {
					if readOnly {
						readUnresolved = append(readUnresolved, value)
					} else {
						unresolved = append(unresolved, value)
					}
				}
			}
			continue
		}
		known = true
		dirs = append(dirs, analysis.Dirs...)
		unresolved = append(unresolved, analysis.Unresolved...)
		providerTargets = append(providerTargets, analysis.ProviderTargets...)
	}
	if known && powerShellMutatesPathContext(ir) {
		return nil, []string{"PowerShell path context"}, providerTargets, true
	}
	if !known {
		unresolved = append(unresolved, readUnresolved...)
	}
	unresolved = append(unresolved, safePowerShellDynamicTargets(ir)...)
	return dedupeSorted(dirs), dedupeSorted(unresolved), dedupeSorted(providerTargets), known
}

// isPowerShellAutomaticConstantExpression reports whether an argument is a
// PowerShell automatic constant such as $true, $false, or $null, or a
// parenthesized one, for example the encoding constructor argument in
// New-Object System.Text.UTF8Encoding($false). These evaluate to a boolean
// or null, never a path, so they must not be presented as runtime targets.
func isPowerShellAutomaticConstantExpression(value string) bool {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "(") && strings.HasSuffix(value, ")") {
		value = value[1 : len(value)-1]
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "$true", "$false", "$null":
		return true
	}
	return false
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
