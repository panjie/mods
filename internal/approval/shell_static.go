package approval

import (
	"strings"

	"mvdan.cc/sh/v3/syntax"
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
	if posix {
		if ro, reason := IsReadOnlyPOSIXWithPolicy(command, policy); ro {
			return ShellStaticAnalysis{Class: ShellStaticRead, Reason: reason}
		}
		result := analyzeShellStaticWrite(command, posix, policy)
		result.UnresolvedPaths = append(result.UnresolvedPaths, unresolvedPOSIXRuntimeExpressions(command)...)
		result.UnresolvedPaths = dedupeSorted(result.UnresolvedPaths)
		return result
	}

	if ro, reason, paths := IsReadOnlyPowerShellWithPolicy(command, policy); ro {
		return ShellStaticAnalysis{
			Class:        ShellStaticRead,
			AffectedDirs: paths,
			Reason:       reason,
		}
	}
	return analyzeShellStaticWrite(command, posix, policy)
}

func unresolvedPOSIXRuntimeExpressions(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var unresolved []string
	syntax.Walk(file, func(node syntax.Node) bool {
		switch node := node.(type) {
		case *syntax.ParamExp:
			if _, known := simpleHomeExpansion(node); known {
				return true
			}
			if node.Param != nil && node.Param.Value != "" {
				unresolved = append(unresolved, "$"+node.Param.Value)
			} else {
				unresolved = append(unresolved, "parameter expansion")
			}
		case *syntax.CmdSubst:
			unresolved = append(unresolved, "command substitution")
		case *syntax.ProcSubst:
			unresolved = append(unresolved, "process substitution")
		}
		return true
	})
	return dedupeSorted(unresolved)
}

// AnalyzeArgvStaticWithPolicy classifies a direct executable invocation whose
// arguments are literal values rather than shell source. Executable paths are
// intentionally not trusted by basename: a workspace binary named like a
// read-only system command may have arbitrary behavior.
func AnalyzeArgvStaticWithPolicy(program string, args []string, posix bool, policy ReadOnlyCommandPolicy) ShellStaticAnalysis {
	program = strings.TrimSpace(program)
	if program == "" || strings.ContainsAny(program, `/\`) {
		return ShellStaticAnalysis{Class: ShellStaticUnknown}
	}
	tokens := append([]string{program}, args...)
	if !posix {
		if policy.matchesPowerShell(program) {
			return ShellStaticAnalysis{Class: ShellStaticRead, Reason: policy.reason(program)}
		}
		tokens[0] = normalizePowerShellCommandName(program)
		policy = ReadOnlyCommandPolicy{}
	}
	if ro, reason := invocationTokensReadOnly(tokens, policy); ro {
		return ShellStaticAnalysis{Class: ShellStaticRead, Reason: reason}
	}
	dirs := analyzeLiteralWritableTargetsFromTokens(tokens, posix).Dirs
	if len(dirs) == 0 && !hasKnownRiskyInvocation(tokens, posix) {
		return ShellStaticAnalysis{Class: ShellStaticUnknown}
	}
	return ShellStaticAnalysis{
		Class:        ShellStaticWrite,
		AffectedDirs: dirs,
		Reason:       "write command (static argv analysis)",
	}
}

func analyzeShellStaticWrite(command string, posix bool, policy ReadOnlyCommandPolicy) ShellStaticAnalysis {
	if !posix {
		if dirs, unresolved, known := analyzePowerShellWritablePaths(command, policy); known {
			return ShellStaticAnalysis{
				Class:           ShellStaticWrite,
				AffectedDirs:    dirs,
				UnresolvedPaths: unresolved,
				Reason:          "write command (PowerShell AST analysis)",
			}
		} else if len(unresolved) > 0 {
			return ShellStaticAnalysis{
				Class:           ShellStaticUnknown,
				UnresolvedPaths: unresolved,
			}
		}
	}
	dirs := ExtractWritableDirs(command, posix)
	if len(dirs) == 0 && !hasKnownRiskyShellCommand(command, posix) {
		return ShellStaticAnalysis{Class: ShellStaticUnknown}
	}
	return ShellStaticAnalysis{
		Class:        ShellStaticWrite,
		AffectedDirs: dirs,
		Reason:       "write command (static analysis)",
	}
}

// analyzePowerShellWritablePaths uses the same parser IR as the read-only
// classifier to inspect every invocation, including commands nested in if
// blocks and semicolon-separated statements. This avoids the lossy fallback
// tokenizer treating a runtime expression such as $PROFILE or $target as a
// workspace-relative directory.
func analyzePowerShellWritablePaths(command string, policy ReadOnlyCommandPolicy) (dirs, unresolved []string, known bool) {
	ir, err := parseWithBridge(command)
	if err != nil || len(ir.ParseErrors) > 0 {
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
				arg = trimPowerShellLiteral(strings.TrimSpace(arg))
				if shellPathExpressionUnresolved(arg, false) {
					unresolved = append(unresolved, arg)
				}
			}
			continue
		}
		known = true
		dirs = append(dirs, analysis.Dirs...)
		unresolved = append(unresolved, analysis.Unresolved...)
	}
	return dedupeSorted(dirs), dedupeSorted(unresolved), known
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
