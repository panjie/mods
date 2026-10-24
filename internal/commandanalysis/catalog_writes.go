package commandanalysis

// Catalog write rules retain the existing operand interpretation. Target roles
// are recorded in InvocationFacts; authorization continues using legacy fields.
func writeRemove(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	operands := writeOperands(args[1:], posix)
	if removeTargetsAreDirs(args[1:], true) {
		return dirTargets(operands), true
	}
	return parentTargets(operands), true
}

func writeDirectory(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	return dirTargets(writeOperands(args[1:], posix)), true
}

func writeParent(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	return parentTargets(writeOperands(args[1:], posix)), true
}

func writeDestination(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if target := targetDirectoryOption(args[1:]); target != "" {
		// -t names an existing target directory. The command writes inside
		// that directory, rather than replacing the directory entry itself.
		return dirTargets([]string{target}), true
	}
	operands := writeOperands(args[1:], posix)
	if len(operands) == 0 {
		return nil, true
	}
	return destinationTargets(operands[len(operands)-1:]), true
}

func writeFind(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if !findHasWriteAction(args[1:], ev) {
		return nil, false
	}
	return dirTargets(findRootOperands(args[1:])), true
}

func writeSort(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if output := sortOutputPath(args[1:]); output != "" {
		return parentTargets([]string{output}), true
	}
	return nil, sortUsesWritableScratch(args[1:])
}

func writeGit(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if output := flagValue(args[1:], "--output"); output != "" {
		return parentTargets([]string{output}), true
	}
	return nil, hasAnyArg(args[1:], "--ext-diff", "--textconv")
}

func writePsRemove(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {

	mode := parentTargets
	if removeTargetsAreDirs(args[1:], false) {
		mode = dirTargets
	}
	if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
		return mode(paths), true
	}
	return mode(writeOperands(args[1:], posix)), true
}

func writePsDestination(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if destinations := powerShellParamValues(args, "destination"); len(destinations) > 0 {
		return destinationTargets(destinations), true
	}
	operands := writeOperands(args[1:], posix)
	if len(operands) == 0 {
		return nil, true
	}
	return destinationTargets(operands[len(operands)-1:]), true
}

func writeLast(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	operands := writeOperands(args[1:], posix)
	if len(operands) == 0 {
		return nil, true
	}
	return destinationTargets(operands[len(operands)-1:]), true
}

func writePsPath(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
		return parentTargets(paths), true
	}
	if paths := firstWriteOperand(args[1:], posix); len(paths) > 0 {
		return parentTargets(paths), true
	}
	return runtimeTarget("PowerShell pipeline target"), true
}

func writePsOut(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	if paths := powerShellParamValues(args, "filepath", "literalpath", "path"); len(paths) > 0 {
		return parentTargets(paths), true
	}
	return parentTargets(firstWriteOperand(args[1:], posix)), true
}

func writeEnv(args []string, posix bool, ev *catalogEvaluation) ([]writableTarget, bool) {
	nested, ok := envCommandArgs(args[1:])
	if !ok {
		return nil, false
	}
	f := ev.processTokens(nested, spaceForShell(posix))
	return f.Writes, f.KnownWrite
}

func parentTargets(paths []string) []writableTarget {
	return targetsWithMode(paths, writableTargetParent)
}
func dirTargets(paths []string) []writableTarget {
	return targetsWithMode(paths, writableTargetDirectory)
}
func destinationTargets(paths []string) []writableTarget {
	return targetsWithMode(paths, writableTargetDestination)
}
func targetsWithMode(paths []string, mode writableTargetMode) []writableTarget {
	result := make([]writableTarget, 0, len(paths))
	for _, p := range paths {
		result = append(result, writableTarget{path: p, mode: mode})
	}
	return result
}
func runtimeTarget(value string) []writableTarget {
	return []writableTarget{{path: value, mode: writableTargetRuntime}}
}
func writeOperands(values []string, posix bool) []string {
	if posix {
		return commandOperands(values)
	}
	return powerShellCommandOperands(values)
}
func firstWriteOperand(values []string, posix bool) []string {
	operands := writeOperands(values, posix)
	if len(operands) == 0 {
		return nil
	}
	return operands[:1]
}
