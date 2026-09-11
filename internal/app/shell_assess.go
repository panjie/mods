package app

// Deterministic shell-command assessment: the dialect-aware pipeline that turns
// a tool call's command text into an approval.CommandAssessment (effect, known
// directories, dynamic targets, reviewability), including the process_run
// invocation path.
//
// Path facts come from two sources, merged here: approval's AST/IR analysis
// (authoritative) and approval's literal-extraction fallback. The LLM
// classifier lives in shell_classify.go and is only consulted when the
// deterministic analysis cannot prove an effect.

import (
	"encoding/json"
	"runtime"
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func shellPathFlavor(tool string) pathutil.Flavor {
	if shellToolUsesPowerShell(tool) {
		return pathutil.FlavorPowerShell
	}
	return pathutil.FlavorPOSIX
}

func shellToolUsesPowerShell(tool string) bool {
	return tool == "powershell_run" || ((tool == "shell_run" || tool == "process_run") && runtime.GOOS == "windows")
}

func (m *Mods) assessCommand(tool, command string) approval.CommandAssessment {
	return m.assessCommandWithEnv(tool, command, nil)
}

// assessCommandWithEnv additionally receives the environment names this
// call injects as secrets; static environment path expansion skips them so
// classification never substitutes a value the child shell will not see.
func (m *Mods) assessCommandWithEnv(tool, command string, shadowedEnv map[string]bool) approval.CommandAssessment {
	ws := ""
	if m.Config != nil {
		ws = m.Config.ResolveWorkspace().Canonical
	}
	return m.assessCommandAtCwd(tool, command, shadowedEnv, ws)
}

func (m *Mods) assessCommandAtCwd(tool, command string, shadowedEnv map[string]bool, ws string) approval.CommandAssessment {
	if tool == "process_run" {
		return m.assessProcessInvocation(command)
	}
	return m.assessShellCommand(tool, shellPathFlavor(tool), command, shadowedEnv, ws)
}

// assessShellCommand runs the deterministic-then-classifier pipeline for one
// shell invocation.
//
// flavor is an explicit parameter rather than being derived from tool inside
// the pipeline. Both analyzers are platform-independent (POSIX goes through
// mvdan/sh, PowerShell through the bridge IR) and path semantics come from
// pathutil, so taking the dialect as input lets tests exercise both dialects on
// any platform; deriving it from the tool name would make the POSIX half
// unreachable on Windows, where every shell tool name selects PowerShell.
func (m *Mods) assessShellCommand(tool string, flavor pathutil.Flavor, command string, shadowedEnv map[string]bool, ws string) approval.CommandAssessment {
	policy := m.readOnlyCommandPolicy()
	posix := flavor != pathutil.FlavorPowerShell
	result := approval.AssessShellStaticWithContext(command, posix, policy, ws)
	result.RemoteOrigins = append(result.RemoteOrigins, extractLiteralRemoteOrigins(command)...)
	gitOrigins, unresolvedGitRemotes := m.resolveGitPushOrigins(tool, command, ws)
	result.RemoteOrigins = append(result.RemoteOrigins, gitOrigins...)
	result.UnresolvedRemoteTargets = append(result.UnresolvedRemoteTargets, unresolvedGitRemotes...)
	result.RemoteOrigins = approval.NormalizeRemoteOrigins(result.RemoteOrigins)
	staticEffect := result.Effect
	// approval's AST/IR analysis is authoritative for shell path facts; the
	// literal extraction is a syntax-independent fallback for the forms
	// command-specific extraction deliberately misses. Both feed one merge, and
	// an authoritative directory is never dropped in favour of the fallback: an
	// omitted external literal must never silently collapse to the workspace
	// approval scope.
	externalPaths := shellExternalPathFacts(result.KnownDirs, ws, flavor)
	extractedPaths, hasBareHome := approval.ExternalShellPathFacts(command, ws, flavor, policy)
	externalPaths = appendMissingShellDirs(externalPaths, extractedPaths)
	if result.Effect == approval.EffectWrite {
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, externalPaths)
	} else {
		result.KnownDirs = externalPaths
	}
	if result.Effect == approval.EffectUnknown {
		completion := approval.UnknownCommandAssessment()
		if m.shellAnalyzer != nil {
			completion = m.shellAnalyzer(tool, command)
		} else {
			completion = m.classifyShellAtCwd(tool, command, ws)
		}
		if hasBareHome {
			// The child shell and path normalizer share HOME. Once an unquoted
			// bare tilde has been resolved locally, classifier-supplied paths are
			// guesses and must not replace or broaden that deterministic scope.
			completion.KnownDirs = nil
		}
		result = mergeCommandAssessment(result, completion)
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
	if result.Effect == approval.EffectRead || result.Effect == approval.EffectWrite {
		allowValueDirs := result.Effect == approval.EffectRead
		if flavor == pathutil.FlavorPowerShell && !result.DynamicProbe && !commandMutatesPowerShellEnvironment(command) {
			result.KnownDirs, result.DynamicTargets = resolvePowerShellEnvTargets(result.KnownDirs, result.DynamicTargets, ws, command, shadowedEnv, allowValueDirs)
			// Engine-automatic variables ($PROFILE / $HOME) resolve in a fresh
			// child shell and can be materialized ahead of approval, turning an
			// unresolvable dynamic target into a concrete, rule-saveable directory.
			resolved := approval.ResolveEngineAutomaticTargets(result.DynamicTargets, probeAssignedSet(result.AssignedVariables))
			result.KnownDirs, result.DynamicTargets = materializeProbeTargets(result.KnownDirs, result.DynamicTargets, resolved)
			// Variables assigned a literal value in the same command resolve to a
			// concrete path as well, so a target such as $p in `$p="C:\x"; Set-Content $p`
			// becomes a reviewable, rule-saveable directory instead of a dynamic target.
			result.KnownDirs, result.DynamicTargets = propagateLiteralTargets(result.KnownDirs, result.DynamicTargets, result.LiteralAssignments, ws)
		} else if flavor == pathutil.FlavorPOSIX && !commandMutatesPOSIXEnvironment(command) {
			result.KnownDirs, result.DynamicTargets = resolvePOSIXEnvTargets(result.KnownDirs, result.DynamicTargets, ws, command, shadowedEnv, allowValueDirs)
		}
	}
	// A statically proven read with no explicit external target operates in the
	// configured workspace context. Classifier-completed commands do not get
	// this fallback: cwd is execution context, not evidence of an affected dir.
	if staticEffect == approval.EffectRead && len(result.KnownDirs) == 0 && len(result.DynamicTargets) == 0 && strings.TrimSpace(ws) != "" {
		result.KnownDirs = []string{ws}
	}
	result = m.verifyScriptExecution(result, ws, flavor)
	return finalizeCommandAssessment(result, flavor)
}

// shellExternalPathFacts keeps the directories approval reported whose location
// is outside the workspace.
//
// For the POSIX dialect it deliberately does not re-judge whether a token
// "looks like" an explicit path: approval already extracted it from a real
// shell parse, and a second heuristic here could only drop facts. Dropping one
// is unsafe because the read branch replaces the fact list wholesale, so an
// emptied list falls back to the workspace scope below — turning an external
// read into a silent workspace read. Measured example: `cat \\server\share\f`
// in the POSIX dialect is reported by approval as external but does not look
// like an explicit path to the older heuristic, and used to collapse to the
// workspace scope.
//
// The PowerShell dialect keeps the explicit-path gate, because there it encodes
// a dialect policy rather than a distrust of approval: leading-slash tokens are
// native-program flags or division, never paths (see
// approval.IsExplicitPowerShellPathArg and the "Unix-style absolute paths are
// ignored" cases in shell_classify_test.go).
func shellExternalPathFacts(dirs []string, ws string, flavor pathutil.Flavor) []string {
	if len(dirs) == 0 {
		return nil
	}
	var result []string
	opts := pathutil.DefaultOptions(ws, flavor)
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if flavor == pathutil.FlavorPowerShell && !approval.IsExplicitPowerShellPathArg(dir) {
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

func mergeCommandAssessment(static, completion approval.CommandAssessment) approval.CommandAssessment {
	if static.Effect != approval.EffectUnknown {
		return static
	}
	if completion.Effect != approval.EffectRead && completion.Effect != approval.EffectWrite {
		return static
	}
	static.Effect = completion.Effect
	static.KnownDirs = appendMissingShellDirs(static.KnownDirs, completion.KnownDirs)
	if strings.TrimSpace(completion.Reason) != "" {
		static.Reason = completion.Reason
	}
	return static
}

func finalizeCommandAssessment(result approval.CommandAssessment, flavor pathutil.Flavor) approval.CommandAssessment {
	result.KnownDirs, result.DynamicTargets = partitionShellAnalysisPaths(
		result.KnownDirs,
		result.DynamicTargets,
		flavor,
	)
	return finalizeAssessmentReviewability(result)
}

// finalizeProcessAssessment deliberately skips shell-expression partitioning:
// process_run arguments are literal, so values such as $HOME/out.txt remain
// concrete cwd-relative paths rather than becoming runtime shell targets.
func finalizeProcessAssessment(result approval.CommandAssessment) approval.CommandAssessment {
	return finalizeAssessmentReviewability(result)
}

func finalizeAssessmentReviewability(result approval.CommandAssessment) approval.CommandAssessment {
	result.RemoteOrigins = approval.NormalizeRemoteOrigins(result.RemoteOrigins)
	reviewability := result.Reviewability
	if result.Effect == approval.EffectWrite && len(result.DynamicTargets) > 0 {
		reviewability.Level = approval.ReviewabilityCompound
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, approval.ReviewabilityDynamicWriteTarget)
		reviewability.ShouldCorrect = true
	}
	if result.Effect == approval.EffectRead && reviewabilityOnlyRecommendsProcess(reviewability) {
		// Keep the process_run recommendation in presentation metadata, but do
		// not spend the request's single corrective round on a harmless read.
		reviewability.ShouldCorrect = false
	}
	if len(result.DynamicTargets) > 1 {
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, approval.ReviewabilityMultipleDynamicTargets)
	}
	result.Reviewability = reviewability
	return result
}

func reviewabilityOnlyRecommendsProcess(reviewability approval.CommandReviewability) bool {
	return len(reviewability.Reasons) == 1 && reviewability.Reasons[0] == approval.ReviewabilitySingleProgramInShell
}

func appendReviewabilityReason(reasons []approval.ReviewabilityReason, reason approval.ReviewabilityReason) []approval.ReviewabilityReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func (m *Mods) assessProcessInvocation(raw string) approval.CommandAssessment {
	var invocation struct {
		Program   string            `json:"program"`
		Args      []string          `json:"args"`
		Cwd       string            `json:"cwd"`
		SecretEnv map[string]string `json:"secret_env"`
	}
	if err := json.Unmarshal([]byte(raw), &invocation); err != nil || strings.TrimSpace(invocation.Program) == "" {
		return approval.UnknownCommandAssessment()
	}
	workspace := ""
	if m.Config != nil {
		workspace = m.Config.ResolveWorkspace().Canonical
	}
	cwd := strings.TrimSpace(invocation.Cwd)
	if cwd == "" {
		cwd = workspace
	} else if !pathutil.IsAbs(cwd) {
		cwd = pathutil.NormalizeShellPath(cwd, pathutil.DefaultOptions(workspace, shellPathFlavor("process_run")))
	}
	flavor := shellPathFlavor("process_run")
	posix := !shellToolUsesPowerShell("process_run")
	policy := m.readOnlyCommandPolicy()
	pathArgs := append([]string{invocation.Program}, invocation.Args...)
	explicitDirs := filterLiteralArgPaths(pathArgs, cwd, flavor)
	environmentKeys := make([]string, 0, len(invocation.SecretEnv))
	for key := range invocation.SecretEnv {
		environmentKeys = append(environmentKeys, key)
	}
	result := approval.AssessArgvStaticWithContext(invocation.Program, invocation.Args, posix, policy, approval.ArgvStaticContext{
		Cwd:             cwd,
		EnvironmentKeys: environmentKeys,
	})
	result.RemoteOrigins = extractLiteralRemoteOrigins(strings.Join(append([]string{invocation.Program}, invocation.Args...), " "))
	if isGitProgram(invocation.Program) {
		gitOrigins, unresolvedGitRemotes := m.resolveGitPushArgvOrigins(invocation.Args, cwd)
		result.RemoteOrigins = append(result.RemoteOrigins, gitOrigins...)
		result.UnresolvedRemoteTargets = append(result.UnresolvedRemoteTargets, unresolvedGitRemotes...)
	}
	result.RemoteOrigins = approval.NormalizeRemoteOrigins(result.RemoteOrigins)
	staticDirs := append([]string(nil), result.KnownDirs...)
	switch result.Effect {
	case approval.EffectRead:
		// A statically proven direct read may use cwd as its implicit target
		// (for example git status), so retain the existing execution-context
		// scope for this proven case.
		result.KnownDirs = appendMissingShellDirs([]string{cwd}, explicitDirs)
	case approval.EffectWrite:
		writeDirs := normalizeLiteralProcessDirs(staticDirs, cwd, flavor)
		result.KnownDirs = appendMissingShellDirs(writeDirs, explicitDirs)
	default:
		classifierInput, _ := json.Marshal(map[string]any{
			"kind":    "direct_process_invocation",
			"program": invocation.Program,
			"args":    invocation.Args,
			"cwd":     cwd,
			"note":    "Arguments are literal; no shell expansion, pipeline, redirection, globbing, or variable interpolation occurs.",
		})
		completion := approval.UnknownCommandAssessment()
		if m.shellAnalyzer != nil {
			completion = m.shellAnalyzer("process_run", string(classifierInput))
		} else {
			completion = m.classifyShellWithLLM("process_run", string(classifierInput))
		}
		// An LLM may recognize that a program mutates state, but it cannot
		// safely bound an arbitrary executable's filesystem effects. In
		// particular, cwd in the classifier input is execution context rather
		// than evidence that the workspace is the mutation target. Preserve
		// effect/reason completion while deriving process directories only from
		// deterministic argv analysis.
		completion.KnownDirs = nil
		result = mergeCommandAssessment(result, completion)
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, explicitDirs)
	}
	// An executable given by an explicit relative or absolute path that lives
	// inside the workspace or a safe directory stays reviewable even when the
	// classifier calls it read-only: the script itself may do anything once it
	// runs. Bare names resolved into those directories are handled separately
	// via the pinned PATH binding in constrainResolvedProcessAssessment.
	if (result.Effect == approval.EffectRead || result.Effect == approval.EffectWrite) &&
		(strings.ContainsAny(invocation.Program, `/\`) || pathutil.IsAbs(invocation.Program)) {
		resolved := strings.TrimSpace(invocation.Program)
		if !pathutil.IsAbs(resolved) {
			resolved = pathutil.NormalizeShellPath(resolved, pathutil.DefaultOptions(cwd, flavor))
		}
		switch pathutil.Location(resolved, workspace, m.safeDirs()) {
		case pathutil.LocationWorkspace, pathutil.LocationSafe:
			result.Effect = approval.EffectUnknown
			result.Reason = "executable resolves from a workspace or temporary directory"
		}
	}
	result = m.verifyScriptExecution(result, cwd, flavor)
	return finalizeProcessAssessment(result)
}

func (m *Mods) constrainResolvedProcessAssessment(result approval.CommandAssessment, binding toolregistry.ProcessProgramBinding) approval.CommandAssessment {
	if binding.Resolved == "" || m == nil || m.Config == nil {
		return result
	}
	workspace := m.Config.ResolveWorkspace().Canonical
	location := pathutil.Location(binding.Resolved, workspace, m.safeDirs())
	if location != pathutil.LocationWorkspace && location != pathutil.LocationSafe {
		return result
	}
	result.Effect = approval.EffectUnknown
	result.Reason = "executable resolves from a workspace or temporary directory"
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
		if _, value, ok := strings.Cut(arg, "="); ok && approval.IsExplicitShellPathArg(value) {
			arg = value
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
	return pathutil.NormalizePath(value, pathutil.Options{Workspace: cwd, Flavor: flavor})
}

func (m *Mods) readOnlyCommandPolicy() approval.ReadOnlyCommandPolicy {
	if m == nil || m.Config == nil {
		return approval.ReadOnlyCommandPolicy{}
	}
	return approval.ReadOnlyCommandPolicy{
		Commands: m.Config.BuiltinTools.ShellReadOnlyCommands,
	}
}

func partitionShellAnalysisPaths(dirs, unresolved []string, flavor pathutil.Flavor) ([]string, []string) {
	posix := flavor != pathutil.FlavorPowerShell
	var concrete []string
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if approval.IsUnresolvedShellPathExpression(dir, posix) {
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
