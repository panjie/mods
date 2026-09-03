package approval

import (
	"strings"

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
	Effect        CommandEffect
	KnownDirs     []string
	RemoteOrigins []string
	// UnresolvedRemoteTargets records remote destinations that are known to
	// exist but cannot be reduced to a deterministic origin.
	UnresolvedRemoteTargets []string
	DynamicTargets          []string
	DynamicProbe            bool
	Reason                  string
	Shape                   CommandShape
	Reviewability           CommandReviewability
	// AssignedVariables lists lowercase normalized names of PowerShell
	// variables assigned within the command (from the parser IR). The app
	// layer uses it to skip probe resolution for targets whose value depends
	// on runtime execution rather than the engine.
	AssignedVariables []string
	// LiteralAssignments maps the lowercase name of a PowerShell variable
	// assigned exactly once at top level with a pure string-literal value (no
	// interpolation) to that literal. The app layer uses it to materialize
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

// AccessIntent converts command facts into the existing tool-neutral policy
// boundary. Unknown effects intentionally map to write.
func (assessment CommandAssessment) AccessIntent() AccessIntent {
	class := AccessWrite
	if assessment.Effect == EffectRead {
		class = AccessRead
	}
	return AccessIntent{
		Class:                   class,
		Dirs:                    append([]string(nil), assessment.KnownDirs...),
		RemoteOrigins:           append([]string(nil), assessment.RemoteOrigins...),
		UnresolvedRemoteTargets: append([]string(nil), assessment.UnresolvedRemoteTargets...),
		UnresolvedPaths:         append([]string(nil), assessment.DynamicTargets...),
		UncertainEffect:         assessment.Effect == EffectUnknown,
		DynamicProbe:            assessment.DynamicProbe,
		Reason:                  assessment.Reason,
	}
}

// AssessShellStaticWithPolicy parses command exactly once and derives every
// deterministic fact available from that syntax tree. Unknown effects are
// intentionally left for the app-layer LLM completion step.
func AssessShellStaticWithPolicy(command string, posix bool, policy ReadOnlyCommandPolicy) CommandAssessment {
	command = strings.TrimSpace(command)
	if command == "" {
		return UnknownCommandAssessment()
	}
	if posix {
		return assessPOSIXStatic(command, policy)
	}
	return assessPowerShellStatic(command, policy)
}

func assessPOSIXStatic(command string, policy ReadOnlyCommandPolicy) CommandAssessment {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil || len(file.Stmts) == 0 {
		result := UnknownCommandAssessment()
		result.Shape.Opaque = true
		result.Reviewability = opaqueReviewability()
		return result
	}

	dynamic := unresolvedPOSIXRuntimeExpressionsFromFile(file)
	shape, reviewability := analyzePOSIXReviewabilityFile(file, policy, dynamic)
	result := CommandAssessment{
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
		result.Effect = EffectRead
		result.KnownDirs = posixAccessArguments(file)
		result.Reason = "read-only command (AST analysis)"
		return result
	}

	dirs, knownWrite := collectPOSIXWriteFacts(file)
	if knownWrite {
		result.Effect = EffectWrite
		result.KnownDirs = dedupeSorted(dirs)
		result.Reason = "write command (static analysis from POSIX AST)"
	}
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

func collectPOSIXWriteFacts(file *syntax.File) (dirs []string, known bool) {
	if file == nil {
		return nil, false
	}
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.Redirect:
			if node.Word == nil || !redirectionWritesPersistent(node) {
				return true
			}
			known = true
			if target, ok := accessShellWord(node.Word); ok && target != "" {
				dirs = append(dirs, parentDir(target))
			}
		case *syntax.CallExpr:
			if len(node.Args) == 0 {
				return true
			}
			if name, ok := staticShellWord(node.Args[0]); ok && hasKnownRiskyInvocation([]string{name}, true) {
				known = true
			}
			tokens := shellWordsForAccess(node.Args)
			if len(tokens) == 0 {
				return true
			}
			analysis := analyzeWritableTargetsFromTokens(tokens, true)
			if analysis.Known || hasKnownRiskyInvocation(tokens, true) {
				known = true
				dirs = append(dirs, analysis.Dirs...)
			}
		}
		return true
	})
	return dedupeSorted(dirs), known
}

func assessPowerShellStatic(command string, policy ReadOnlyCommandPolicy) CommandAssessment {
	ir, err := parseWithBridge(command)
	if err != nil || len(ir.ParseErrors) > 0 {
		result := UnknownCommandAssessment()
		result.Shape.Opaque = true
		result.Reviewability = opaqueReviewability()
		return result
	}
	return assessPowerShellIR(command, ir, policy)
}

func assessPowerShellIR(command string, ir *psBridgeIR, policy ReadOnlyCommandPolicy) CommandAssessment {
	dirs, dynamic, knownWrite := analyzePowerShellWritablePathsIR(ir, policy)
	shape, reviewability := analyzePowerShellReviewabilityIR(ir, policy, dynamic)
	result := CommandAssessment{
		Effect:             EffectUnknown,
		DynamicTargets:     dynamic,
		Reason:             "effects could not be proven",
		Shape:              shape,
		Reviewability:      reviewability,
		AssignedVariables:  assignedPowerShellVariables(ir),
		LiteralAssignments: powerShellLiteralAssignments(ir),
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
	return result
}

// assignedPowerShellVariables returns the lowercase normalized names of every
// variable assigned within the command, whether at top level or inside a
// script block. The result feeds CommandAssessment.AssignedVariables so the
// app-layer probe can avoid resolving a reference whose value the command
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
