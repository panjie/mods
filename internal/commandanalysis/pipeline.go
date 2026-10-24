package commandanalysis

// Deterministic shell-command assessment: the dialect-aware pipeline that turns
// a tool call's command text into a CommandAssessment (effect, known
// directories, dynamic targets, reviewability), including the process_run
// invocation path.
//
// Path facts come from two sources, merged here: AST/IR analysis
// (authoritative) and the literal-extraction fallback.

import (
	"path/filepath"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// analyzeShell builds deterministic facts for one
// shell invocation.
//
// flavor is an explicit parameter rather than being derived from tool inside
// the pipeline. Both analyzers are platform-independent (POSIX goes through
// mvdan/sh, PowerShell through the bridge IR) and path semantics come from
// pathutil, so taking the dialect as input lets tests exercise both dialects on
// any platform; deriving it from the tool name would make the POSIX half
// unreachable on Windows, where every shell tool name selects PowerShell.
func (m *analysisRun) analyzeShell(flavor pathutil.Flavor, command string, shadowedEnv map[string]bool, ws string) CommandAssessment {
	policy := m.policy
	p := m.parse(command, flavor)
	result := p.assess(policy, ArgvStaticContext{Cwd: ws, run: m})
	if p.err != nil {
		result.AddGap(GapParse)
		if m.failure != nil || m.ctx.Err() != nil {
			return result
		}
	}
	result.RemoteOrigins = append(result.RemoteOrigins, extractLiteralRemoteOrigins(command)...)
	gitOrigins, unresolvedGitRemotes, gitDirs := m.resolveParsedGitPushOrigins(p, ws)
	result.RemoteOrigins = append(result.RemoteOrigins, gitOrigins...)
	result.KnownDirs = appendMissingShellDirs(result.KnownDirs, gitDirs)
	result.UnresolvedRemoteTargets = append(result.UnresolvedRemoteTargets, unresolvedGitRemotes...)
	result.RemoteOrigins = NormalizeRemoteOrigins(result.RemoteOrigins)
	staticEffect := result.Effect
	// AST/IR analysis is authoritative for shell path facts; the
	// literal extraction is a syntax-independent fallback for the forms
	// command-specific extraction deliberately misses. Both feed one merge, and
	// an authoritative directory is never dropped in favour of the fallback: an
	// omitted external literal must never silently collapse to the cwd
	// approval scope.
	externalPaths := shellExternalPathFactsOptions(result.KnownDirs, ws, flavor, m.options(ws, flavor))
	var extractedPaths []string
	if !result.PowerShellPathContextMutation {
		extractedPaths = externalShellPathFacts(p, ws, flavor, result.Effect == EffectRead, m.options(ws, flavor))
	}
	externalPaths = appendMissingShellDirs(externalPaths, extractedPaths)
	if result.Effect == EffectWrite {
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, externalPaths)
	} else {
		result.KnownDirs = appendMissingShellDirs(externalPaths, gitDirs)
	}
	// Path-shaped references to inherited environment variables resolve
	// deterministically for the child shell whenever the command does not
	// mutate the environment and the name is not shadowed by this call's
	// secret environment. Materializing them as concrete directories makes
	// the approval scope rule-saveable instead of an unresolvable dynamic
	// target, lets temp-dir writes such as Set-Content "$env:TEMP\file"
	// reach the safe-dir allow cell of the approval matrix, and stops bare
	// references that only ever appear in path-shaped uses (for example
	// $env:USERPROFILE inside "$env:USERPROFILE\.emacs.d\init.el") from
	// re-dynamizing the intent. Probes keep their dynamic-target auto-allow
	// semantics, and commands that reassign the environment never expand:
	// the child would observe a different value.
	{
		allowValueDirs := result.Effect == EffectRead
		if flavor == pathutil.FlavorPowerShell && !result.DynamicProbe && !commandMutatesPowerShellEnvironment(command) {
			result.KnownDirs, result.DynamicTargets = resolvePowerShellEnvTargetsOptions(result.KnownDirs, result.DynamicTargets, ws, command, shadowedEnv, allowValueDirs, m.options(ws, flavor))
			// Engine-automatic variables ($PROFILE / $HOME) resolve in a fresh
			// child shell and can be materialized ahead of approval, turning an
			// unresolvable dynamic target into a concrete, rule-saveable directory.
			resolved := m.resolveAutomatics(result.DynamicTargets, probeAssignedSet(result.AssignedVariables))
			result.KnownDirs, result.DynamicTargets = materializeProbeTargets(result.KnownDirs, result.DynamicTargets, resolved)
			// Variables assigned a literal value in the same command resolve to a
			// concrete path as well, so a target such as $p in `$p="C:\x"; Set-Content $p`
			// becomes a reviewable, rule-saveable directory instead of a dynamic target.
			var literalProviders []string
			result.KnownDirs, result.DynamicTargets, literalProviders = propagateLiteralTargetsOptions(result.KnownDirs, result.DynamicTargets, result.LiteralAssignments, ws, m.options(ws, flavor))
			if result.Effect == EffectWrite {
				result.ProviderWriteTargets = appendMissingShellDirs(result.ProviderWriteTargets, literalProviders)
			}
		} else if flavor == pathutil.FlavorPOSIX && !posixEnvironmentMutation(command, p.file) {
			result.KnownDirs, result.DynamicTargets = resolvePOSIXEnvTargetsOptions(result.KnownDirs, result.DynamicTargets, ws, command, shadowedEnv, allowValueDirs, m.options(ws, flavor))
		}
	}
	// A statically proven read with no explicit external target operates in the
	// current working directory context. Unknown commands do not get
	// this fallback: cwd is execution context, not evidence of an affected dir.
	if staticEffect == EffectRead && len(result.KnownDirs) == 0 && len(result.DynamicTargets) == 0 && strings.TrimSpace(ws) != "" {
		result.KnownDirs = []string{ws}
	}
	result = m.resolveAssessmentWritePaths(result, ws, flavor, false)
	return finalizeCommandAssessment(result, flavor)
}

// shellExternalPathFacts, which applies the same policy to the parser's static
// facts only: literal argv directories never pass
// through that gate.
func filterPowerShellDialectDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	kept := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		trimmed := strings.TrimSpace(dir)
		if strings.HasPrefix(trimmed, "/") && !IsExplicitPowerShellPathArg(trimmed) {
			continue
		}
		kept = append(kept, dir)
	}
	return kept
}

// shellExternalPathFacts keeps the directories the parser reported whose location
// is outside the cwd.
//
// For the POSIX dialect it deliberately does not re-judge whether a token
// "looks like" an explicit path: the parser already extracted it from a real
// shell parse, and a second heuristic here could only drop facts. Dropping one
// is unsafe because the read branch replaces the fact list wholesale, so an
// emptied list falls back to the cwd scope below — turning an external
// read into a silent cwd read. Measured example: `cat \\server\share\f`
// in the POSIX dialect is reported by the parser as external but does not look
// like an explicit path to the older heuristic, and used to collapse to the
// cwd scope.
//
// The PowerShell dialect keeps the explicit-path gate, because there it encodes
// a dialect policy rather than a distrust of the parser: leading-slash tokens are
// native-program flags or division, never paths (see
// IsExplicitPowerShellPathArg and the "Unix-style absolute paths are
// ignored" cases in pipeline_helpers_test.go).
func shellExternalPathFacts(dirs []string, ws string, flavor pathutil.Flavor) []string {
	return shellExternalPathFactsOptions(dirs, ws, flavor, pathutil.DefaultOptions(ws, flavor))
}
func shellExternalPathFactsOptions(dirs []string, ws string, flavor pathutil.Flavor, opts pathutil.Options) []string {
	if len(dirs) == 0 {
		return nil
	}
	var result []string
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if flavor == pathutil.FlavorPowerShell && !IsExplicitPowerShellPathArg(dir) {
			continue
		}
		normalized := pathutil.NormalizeShellPath(dir, opts)
		if pathutil.Location(normalized, ws, nil) != pathutil.LocationExternal {
			continue
		}
		result = appendMissingShellDirs(result, []string{normalized})
	}
	return result
}

func finalizeCommandAssessment(result CommandAssessment, flavor pathutil.Flavor) CommandAssessment {
	if flavor == pathutil.FlavorPowerShell {
		var providers []string
		result.KnownDirs, providers = partitionPowerShellProviderTargets(result.KnownDirs)
		if result.Effect != EffectRead {
			result.ProviderWriteTargets = appendMissingShellDirs(result.ProviderWriteTargets, providers)
		}
	}
	result.KnownDirs, result.DynamicTargets = partitionShellAnalysisPaths(
		result.KnownDirs,
		result.DynamicTargets,
		flavor,
	)
	return finalizeAssessmentReviewability(result)
}

func partitionPowerShellProviderTargets(targets []string) ([]string, []string) {
	var filesystem, providers []string
	for _, target := range targets {
		if filesystemPath, ok := PowerShellFilesystemProviderPath(target); ok {
			filesystem = appendMissingShellDirs(filesystem, []string{filesystemPath})
			continue
		}
		if IsPowerShellProviderPath(target) {
			providers = appendMissingShellDirs(providers, []string{strings.TrimSpace(target)})
			continue
		}
		filesystem = appendMissingShellDirs(filesystem, []string{target})
	}
	return filesystem, providers
}

// finalizeProcessAssessment deliberately skips shell-expression partitioning:
// process_run arguments are literal, so values such as $HOME/out.txt remain
// concrete cwd-relative paths rather than becoming runtime shell targets.
func finalizeProcessAssessment(result CommandAssessment) CommandAssessment {
	return finalizeAssessmentReviewability(result)
}

func finalizeAssessmentReviewability(result CommandAssessment) CommandAssessment {
	result.RemoteOrigins = NormalizeRemoteOrigins(result.RemoteOrigins)
	reviewability := result.Reviewability
	if result.Effect == EffectWrite && len(result.DynamicTargets) > 0 {
		reviewability.Level = ReviewabilityCompound
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, ReviewabilityDynamicWriteTarget)
	}
	if len(result.DynamicTargets) > 1 {
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, ReviewabilityMultipleDynamicTargets)
	}
	result.Reviewability = reviewability
	return result
}

func appendReviewabilityReason(reasons []ReviewabilityReason, reason ReviewabilityReason) []ReviewabilityReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

// Preserve lexical facts, and add the real leaf's parent before any temp or
// saved-rule decision. Runtime expressions cannot establish a reusable scope.
func (m *analysisRun) resolveAssessmentWritePaths(result CommandAssessment, cwd string, flavor pathutil.Flavor, literal bool) CommandAssessment {
	for _, raw := range result.WritePaths {
		if !literal && strings.Contains(raw, "$") && len(result.DynamicTargets) == 0 {
			opts := m.options(cwd, flavor)
			opts.Env["PWD"] = cwd
			if expanded, ok := pathutil.ExpandEnvPath(raw, opts); ok {
				raw = expanded
			}
		}
		if !literal && strings.ContainsAny(raw, "$`*?[]") {
			result.AddGap(GapWriteTargets)
			continue
		}
		path := pathutil.NormalizeShellPath(raw, m.options(cwd, flavor))
		if literal {
			path = normalizeLiteralProcessPath(raw, cwd, flavor)
		}
		if !filepath.IsAbs(path) {
			result.AddGap(GapWriteTargets)
			continue
		}
		real, err := m.resolvePath(path)
		if err != nil {
			result.AddGap(GapWriteTargets)
			continue
		}
		if real != path {
			result.KnownDirs = appendMissingShellDirs(result.KnownDirs, []string{filepath.Dir(real)})
		}
	}
	return result
}

func normalizeLiteralProcessDirs(dirs []string, cwd string, flavor pathutil.Flavor) []string {
	if len(dirs) == 0 {
		return nil
	}
	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if normalized := normalizeLiteralProcessPath(dir, cwd, flavor); normalized != "" {
			result = appendMissingShellDirs(result, []string{normalized})
		}
	}
	return result
}

func filterLiteralArgPaths(args []string, cwd string, flavor pathutil.Flavor) []string {
	var result []string
	for _, arg := range args {
		arg = strings.Trim(strings.TrimSpace(arg), `"'`)
		if _, value, ok := strings.Cut(arg, "="); ok && IsExplicitShellPathArg(value) {
			arg = value
		}
		if flavor == pathutil.FlavorPowerShell && IsPowerShellProviderPath(arg) {
			continue
		}
		if flavor == pathutil.FlavorPowerShell {
			if filesystemPath, ok := PowerShellFilesystemProviderPath(arg); ok {
				arg = filesystemPath
			}
		}
		if !literalArgLooksPathLike(arg, flavor) {
			continue
		}
		normalized := normalizeLiteralProcessPath(arg, cwd, flavor)
		if pathutil.Location(normalized, cwd, nil) == pathutil.LocationExternal {
			result = appendMissingShellDirs(result, []string{normalized})
		}
	}
	return result
}

func literalArgLooksPathLike(arg string, flavor pathutil.Flavor) bool {
	if arg == "" {
		return false
	}
	if flavor == pathutil.FlavorPowerShell && strings.HasPrefix(arg, "/") && !IsExplicitPowerShellPathArg(arg) {
		// pathutil.IsAbs treats any leading slash as an absolute path (POSIX
		// semantics). In the PowerShell dialect that syntax belongs to
		// native-program switches such as reg.exe /d or csc /out; only a
		// forward-slash UNC path (//server/share) is an explicit path.
		return false
	}
	if pathutil.IsAbs(arg) || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`) {
		return true
	}
	if flavor == pathutil.FlavorPowerShell {
		return strings.ContainsAny(arg, `/\`)
	}
	return strings.Contains(arg, "/")
}

func normalizeLiteralProcessPath(value, cwd string, flavor pathutil.Flavor) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !pathutil.IsAbs(value) {
		// Prefixing with an explicit current-directory segment prevents pathutil
		// from interpreting shell-looking literals such as $HOME/x or ~/x.
		if flavor == pathutil.FlavorPowerShell {
			value = `.\` + value
		} else {
			value = "./" + value
		}
	}
	return pathutil.NormalizePath(value, pathutil.Options{WorkingDir: cwd, Flavor: flavor})
}

func partitionShellAnalysisPaths(dirs, unresolved []string, flavor pathutil.Flavor) ([]string, []string) {
	posix := flavor != pathutil.FlavorPowerShell
	var concrete []string
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if IsUnresolvedShellPathExpression(dir, posix) {
			unresolved = append(unresolved, dir)
			continue
		}
		concrete = appendMissingShellDirs(concrete, []string{dir})
	}
	var dynamic []string
	seen := map[string]struct{}{}
	for _, expr := range unresolved {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		key := expr
		if flavor == pathutil.FlavorPowerShell {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dynamic = append(dynamic, expr)
	}
	return concrete, dynamic
}

func appendMissingShellDirs(dirs []string, extra []string) []string {
	for _, p := range extra {
		found := false
		for _, d := range dirs {
			if d == p {
				found = true
				break
			}
		}
		if !found {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// probeAssignedSet converts the assessment's normalized assigned-variable
// names into a lookup set for the probe eligibility check.
func probeAssignedSet(assigned []string) map[string]bool {
	if len(assigned) == 0 {
		return nil
	}
	set := make(map[string]bool, len(assigned))
	for _, name := range assigned {
		set[strings.ToLower(strings.TrimSpace(name))] = true
	}
	return set
}

// materializeProbeTargets moves probe-resolved dynamic targets into the
// concrete known directories. Each resolved target maps its original
// expression to an absolute path; resolved expressions are dropped from the
// dynamic list and their paths appended to known, mirroring
// resolvePowerShellEnvTargets so downstream parent-directory normalization and
// rule generation see concrete paths.
func materializeProbeTargets(known, dynamic []string, resolved map[string]string) ([]string, []string) {
	if len(resolved) == 0 || len(dynamic) == 0 {
		return known, dynamic
	}
	kept := make([]string, 0, len(dynamic))
	var added []string
	for _, target := range dynamic {
		if path, ok := resolved[strings.TrimSpace(target)]; ok {
			added = appendMissingShellDirs(added, []string{path})
			continue
		}
		kept = append(kept, target)
	}
	if len(added) == 0 {
		return known, dynamic
	}
	return appendMissingShellDirs(known, added), kept
}

func (m *analysisRun) analyzeProcess(invocation ProcessInput, cwd string, flavor pathutil.Flavor) CommandAssessment {
	baseDir := m.workingDir
	posix := flavor != pathutil.FlavorPowerShell
	policy := m.policy
	// The program's own path is execution context, not an affected directory:
	// an external executable's install location must never become a review
	// target or a "write dirs" approval candidate. Only literal arguments
	// are path facts.
	explicitDirs := filterLiteralArgPaths(invocation.Args, cwd, flavor)
	environmentKeys := invocation.EnvironmentKeys
	result := AssessArgvStaticWithContext(invocation.Program, invocation.Args, posix, policy, ArgvStaticContext{
		Cwd:             cwd,
		EnvironmentKeys: environmentKeys,
		run:             m,
	})
	result.RemoteOrigins = extractLiteralRemoteOrigins(strings.Join(append([]string{invocation.Program}, invocation.Args...), " "))
	if isGitProgram(invocation.Program) {
		gitOrigins, unresolvedGitRemotes, gitDirs := m.resolveGitPushArgvOrigins(invocation.Args, cwd)
		result.RemoteOrigins = append(result.RemoteOrigins, gitOrigins...)
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, gitDirs)
		result.UnresolvedRemoteTargets = append(result.UnresolvedRemoteTargets, unresolvedGitRemotes...)
	}
	result.RemoteOrigins = NormalizeRemoteOrigins(result.RemoteOrigins)
	staticDirs := append([]string(nil), result.KnownDirs...)
	switch result.Effect {
	case EffectRead:
		// A statically proven direct read may use cwd as its implicit target
		// (for example git status), so retain the existing execution-context
		// scope for this proven case.
		result.KnownDirs = appendMissingShellDirs([]string{cwd}, explicitDirs)
	case EffectWrite:
		writeDirs := normalizeLiteralProcessDirs(staticDirs, cwd, flavor)
		result.KnownDirs = appendMissingShellDirs(writeDirs, explicitDirs)
	default:
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, explicitDirs)
	}
	// An executable given by an explicit relative or absolute path that lives
	// inside the cwd or a safe directory stays reviewable because the
	// script itself may do anything once it
	// runs. Bare names resolved into those directories are handled separately
	// via the pinned PATH binding in constrainResolvedProcessAssessment.
	if strings.ContainsAny(invocation.Program, `/\`) || pathutil.IsAbs(invocation.Program) {
		resolved := strings.TrimSpace(invocation.Program)
		if !pathutil.IsAbs(resolved) {
			resolved = pathutil.NormalizeShellPath(resolved, m.options(cwd, flavor))
		}
		switch pathutil.Location(resolved, baseDir, m.programDirs) {
		case pathutil.LocationWorkingDir, pathutil.LocationSafe:
			result.Effect = EffectUnknown
			result.Evidence = EvidenceUnknown
			result.AddGap(GapExecutionContext)
			result.Reason = "executable resolves from a cwd or temporary directory"
		}
	}
	result = m.resolveAssessmentWritePaths(result, cwd, flavor, true)
	return finalizeProcessAssessment(result)
}
