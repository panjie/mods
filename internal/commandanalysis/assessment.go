package commandanalysis

import (
	ctxpkg "context"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
	"mvdan.cc/sh/v3/syntax"
)

// CommandEffect is the persistent-state effect proven for a command. Unknown
// is distinct from write for presentation, but maps to write access so policy
// decisions remain fail-closed.
type CommandEffect string

const (
	EffectRead    CommandEffect = "read"
	EffectWrite   CommandEffect = "write"
	EffectUnknown CommandEffect = "unknown"
)

// CommandShape contains parser-derived structure used only for reviewability.
type CommandShape struct {
	TopLevelActions int
	Pipelines       int
	Opaque          bool
}

// CommandAssessment is the single fact bundle produced for a shell or direct
// process invocation. Approval policy is derived from it and never stored in
// the assessment itself.
type CommandAssessment struct {
	Targets       []Target
	Evidence      Evidence
	WriteCoverage TargetCoverage
	Gaps          []AssessmentGap
	// StaticRead is set only by deterministic analysis.
	StaticRead bool
	Effect     CommandEffect
	KnownDirs  []string
	// WritePaths retains leaf operands until the app resolves filesystem links.
	WritePaths []string
	// ProviderWriteTargets records non-filesystem PowerShell provider paths.
	// They are exact review targets but never become filesystem DirAllow rules.
	ProviderWriteTargets []string
	RemoteOrigins        []string
	// UnresolvedRemoteTargets records remote destinations that are known to
	// exist but cannot be reduced to a deterministic origin.
	UnresolvedRemoteTargets       []string
	DynamicTargets                []string
	DynamicProbe                  bool
	PowerShellPathContextMutation bool
	Reason                        string
	Shape                         CommandShape
	Reviewability                 CommandReviewability
	// AssignedVariables lists lowercase normalized names of PowerShell
	// variables assigned within the command (from the parser IR). The analysis
	// pipeline uses it to skip probe resolution for targets whose value depends
	// on runtime execution rather than the engine.
	AssignedVariables []string
	// LiteralAssignments maps the lowercase name of a PowerShell variable
	// assigned exactly once at top level with a pure string-literal value (no
	// interpolation) to that literal. The analysis pipeline uses it to materialize
	// dynamic targets that reference the variable. Nil for POSIX and for
	// commands with no such assignments.
	LiteralAssignments map[string]string
}

func UnknownCommandAssessment() CommandAssessment {
	return CommandAssessment{
		Effect: EffectUnknown,
		Reason: "effects could not be proven",
	}
}

// AssessShellStaticWithPolicy parses command exactly once and derives every
// deterministic fact available from that syntax tree. Unknown effects are
// retained and require ordinary approval. The cwd-less form cannot resolve
// Git repository directories; callers that know the
// execution directory should use AssessShellStaticWithContext.
func AssessShellStaticWithPolicy(command string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	return AssessShellStaticWithContext(command, posix, policy, "")
}

// AssessShellStaticWithContext additionally receives the deterministic
// working directory the shell runs in. It lets Git cwd-write
// invocations resolve their repository directories (worktree, Git
// administrative storage) the same way direct argv invocations do. An empty
// cwd disables that enrichment fail-closed.
func AssessShellStaticWithContext(command string, posix bool, policy ReadOnlyCommandPolicy, cwd string) CommandAssessment {
	return parseCommandLegacy(command, posix).assess(policy, ArgvStaticContext{Cwd: cwd})
}

func (p *parsedCommand) assess(policy ReadOnlyCommandPolicy, context ArgvStaticContext) CommandAssessment {
	if strings.TrimSpace(p.source) == "" {
		return UnknownCommandAssessment()
	}
	if p.err != nil || (p.file == nil && p.ir == nil) {
		return opaqueAssessment()
	}
	if context.run == nil {
		context.run = NewEngine(Settings{}, Dependencies{}).run(ctxpkg.Background())
	}
	p.semantics = newSemanticSession(p, policy)
	p.semantics.context = context
	context.semantics = p.semantics
	analyze := func(policy ReadOnlyCommandPolicy) CommandAssessment {
		policy.session = p.semantics
		if p.file != nil {
			return assessPOSIXFile(p.file, policy, context)
		}
		return assessPowerShellIRContext(p.source, p.ir, policy, context)
	}
	result := analyze(policy)
	run := context.run
	flavor := pathutil.FlavorPOSIX
	if p.ir != nil {
		flavor = pathutil.FlavorPowerShell
	}
	opts := run.options(context.Cwd, flavor)
	for i, value := range result.KnownDirs {
		if strings.HasPrefix(value, "$HOME") || strings.HasPrefix(value, "${HOME}") {
			result.KnownDirs[i] = pathutil.ExpandToken(value, opts)
		}
	}
	for i, value := range result.WritePaths {
		if strings.HasPrefix(value, "$HOME") || strings.HasPrefix(value, "${HOME}") {
			result.WritePaths[i] = pathutil.ExpandToken(value, opts)
		}
	}
	result.StaticRead = result.Effect == EffectRead
	if result.Effect != EffectUnknown {
		result.Evidence = EvidenceStatic
	}
	if result.Effect == EffectRead && len(policy.Commands) > 0 && analyze(ReadOnlyCommandPolicy{}).Effect != EffectRead {
		result.Evidence = EvidenceTrust
	}
	return result
}

func opaqueAssessment() CommandAssessment {
	result := UnknownCommandAssessment()
	result.Shape.Opaque = true
	result.Reviewability = opaqueReviewability()
	return result
}

func assessPOSIXStatic(command string, policy ReadOnlyCommandPolicy, cwd string) CommandAssessment {
	return parseCommandLegacy(command, true).assess(policy, ArgvStaticContext{Cwd: cwd})
}

func assessPOSIXFile(file *syntax.File, policy ReadOnlyCommandPolicy, context ArgvStaticContext) (result CommandAssessment) {
	if policy.session == nil {
		policy.session = newSemanticSession(&parsedCommand{file: file}, policy)
	}
	defer func() {
		if file != nil {
			appendPOSIXTargets(&result, file, policy.session)
		}
	}()
	if file == nil || len(file.Stmts) == 0 {
		return opaqueAssessment()
	}

	dynamic := unresolvedPOSIXRuntimeExpressionsFromFile(file)
	shape, reviewability := analyzePOSIXReviewabilityFile(file, policy, dynamic)
	result = CommandAssessment{
		Effect:         EffectUnknown,
		DynamicTargets: dynamic,
		Reason:         "effects could not be proven",
		Shape:          shape,
		Reviewability:  reviewability,
	}

	readOnly := true
	for _, stmt := range file.Stmts {
		if ro, _ := stmtIsReadOnly(stmt, policy); !ro {
			readOnly = false
			break
		}
	}
	if readOnly {
		result.WriteCoverage = CoverageComplete
		result.Effect = EffectRead
		result.KnownDirs = posixAccessArguments(file)
		result.Reason = "read-only command (AST analysis)"
		return result
	}

	dirs, knownWrite := collectPOSIXWriteFactsContext(file, context)
	if knownWrite {
		result.Effect = EffectWrite
		result.KnownDirs = dedupeSorted(dirs)
		result.Reason = "write command (static analysis from POSIX AST)"
	}
	assessPOSIXCoverage(&result, file, policy)
	return result
}

func posixAccessArguments(file *syntax.File) []string {
	if file == nil {
		return nil
	}
	var args []string
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		for _, word := range call.Args[1:] {
			if value, ok := accessShellWord(word); ok && value != "" {
				args = append(args, value)
			}
		}
		return true
	})
	return dedupeSorted(args)
}

func collectPOSIXWriteFacts(file *syntax.File, cwd string) (dirs []string, known bool) {
	return collectPOSIXWriteFactsContext(file, ArgvStaticContext{Cwd: cwd})
}
func collectPOSIXWriteFactsContext(file *syntax.File, context ArgvStaticContext) (dirs []string, known bool) {
	if file == nil {
		return nil, false
	}
	if context.semantics == nil {
		context.semantics = newSemanticSession(&parsedCommand{file: file}, ReadOnlyCommandPolicy{})
	}
	confinementOK := context.semantics.confined
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Redirect:
			if node.Word == nil || !redirectionWritesPersistent(node) {
				return true
			}
			known = true
			if target, ok := accessShellWordConfined(node.Word, confinementOK); ok && target != "" {
				dirs = append(dirs, parentDir(target))
			}
		case *syntax.CallExpr:
			if len(node.Args) == 0 {
				return true
			}
			f := context.semantics.posix(node)
			tokens := shellWordsForAccessConfined(node.Args, confinementOK)
			if gitResult := context.semantics.git(f, tokens, true, context); gitResult != nil {
				known = true
				dirs = append(dirs, gitResult.KnownDirs...)
				return true
			}
			analysis := analyzeWritableTargets(f.Writes, f.KnownWrite, true, true, context.pathOptions(true))
			if f.KnownWrite || f.Risky {
				known = true
				dirs = append(dirs, analysis.Dirs...)
			}
		}
		return true
	})
	return dedupeSorted(dirs), known
}

func assessPowerShellStatic(command string, policy ReadOnlyCommandPolicy, cwd string) CommandAssessment {
	return parseCommandLegacy(command, false).assess(policy, ArgvStaticContext{Cwd: cwd})
}

func assessPowerShellIR(command string, ir *psBridgeIR, policy ReadOnlyCommandPolicy, cwd string) CommandAssessment {
	return assessPowerShellIRContext(command, ir, policy, ArgvStaticContext{Cwd: cwd})
}
func assessPowerShellIRContext(command string, ir *psBridgeIR, policy ReadOnlyCommandPolicy, context ArgvStaticContext) CommandAssessment {
	if policy.session == nil {
		policy.session = newSemanticSession(nil, policy)
	}
	context.semantics = policy.session
	dirs, dynamic, providerTargets, knownWrite := analyzePowerShellWritablePathsContext(ir, policy, context)
	shape, reviewability := analyzePowerShellReviewabilityIR(ir, policy, dynamic)
	result := CommandAssessment{
		Effect:                        EffectUnknown,
		DynamicTargets:                dynamic,
		ProviderWriteTargets:          providerTargets,
		PowerShellPathContextMutation: powerShellMutatesPathContext(ir),
		Reason:                        "effects could not be proven",
		Shape:                         shape,
		Reviewability:                 reviewability,
		AssignedVariables:             assignedPowerShellVariables(ir),
		LiteralAssignments:            powerShellLiteralAssignments(ir),
	}
	if readOnlyPowerShellIR(command, ir, policy) {
		result.Effect = EffectRead
		result.KnownDirs = append([]string(nil), ir.Paths...)
		result.DynamicProbe = len(dynamic) > 0 && powerShellDynamicTargetProbe(ir, dynamic)
		result.Reason = "read-only PowerShell command (PowerShell AST static analysis)"
	} else if knownWrite {
		result.Effect = EffectWrite
		result.KnownDirs = dirs
		result.Reason = "write command (PowerShell AST static analysis)"
	}
	for _, inv := range ir.Invocations {
		start := len(result.Targets)
		appendInvocationTargets(&result, policy.session.ps(inv))
		for i := start; i < len(result.Targets); i++ {
			result.Targets[i].NodeID = inv.NodeID
			single, _ := powerShellArgQuoting(result.Targets[i].Raw)
			result.Targets[i].Literal = single
		}
	}
	assessPowerShellCoverage(&result, ir, policy)
	if result.Effect != EffectUnknown {
		result.Evidence = EvidenceStatic
	}
	return result
}

// assignedPowerShellVariables returns the lowercase normalized names of every
// variable assigned within the command, whether at top level or inside a
// script block. The result feeds CommandAssessment.AssignedVariables so the
// analysis probe can avoid resolving a reference whose value the command
// itself sets at runtime.
func assignedPowerShellVariables(ir *psBridgeIR) []string {
	if ir == nil {
		return nil
	}
	seen := map[string]bool{}
	var names []string
	for _, list := range [][]string{ir.AssignmentTargets, ir.ScriptBlockAssignmentTargets} {
		for _, target := range list {
			name := normalizePowerShellVariableName(target)
			if seen[name] {
				continue
			}
			seen[name] = true
			names = append(names, name)
		}
	}
	return dedupeSorted(names)
}

func powerShellDynamicTargetProbe(ir *psBridgeIR, dynamic []string) bool {
	if ir == nil {
		return false
	}
	envVarTarget := false
	for _, target := range dynamic {
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(target)), "$env:") {
			envVarTarget = true
			break
		}
	}
	for _, inv := range ir.Invocations {
		name := normalizePowerShellCommandName(inv.Name)
		if !powerShellProbeCommands[name] {
			return false
		}
		if envVarTarget && powerShellOutputProbeCommands[name] {
			// Output-emitting probes echo the environment variable's value
			// into the model context, so they are content reads rather than
			// path-resolution probes and must not auto-allow.
			return false
		}
	}
	return len(ir.Invocations) > 0 || len(ir.TopLevelValueExpressions) > 0
}

var powerShellProbeCommands = map[string]bool{
	"test-path": true, "resolve-path": true, "convert-path": true,
	"join-path": true, "split-path": true,
	"write-output": true, "write": true, "echo": true,
	"select-object": true, "select": true,
	"format-table": true, "ft": true, "format-list": true, "fl": true,
	"format-wide": true, "fw": true, "format-custom": true, "fc": true,
	"out-string": true, "out-host": true, "out-null": true,
	"convertto-json": true,
}

// powerShellOutputProbeCommands is the subset of powerShellProbeCommands that
// puts argument values into the output stream. Combined with a bare $env:NAME
// target such a probe would echo the variable's value into the model context,
// so it never qualifies for the auto-allow DynamicProbe treatment.
var powerShellOutputProbeCommands = map[string]bool{
	"write-output": true, "write": true, "echo": true,
	"select-object": true, "select": true,
	"format-table": true, "ft": true, "format-list": true, "fl": true,
	"format-wide": true, "fw": true, "format-custom": true, "fc": true,
	"out-string": true, "out-host": true,
	"convertto-json": true,
}
