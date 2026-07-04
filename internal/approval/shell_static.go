package approval

type ShellStaticClass string

const (
	ShellStaticUnknown ShellStaticClass = "unknown"
	ShellStaticRead    ShellStaticClass = "read"
	ShellStaticWrite   ShellStaticClass = "write"
)

type ShellStaticAnalysis struct {
	Class        ShellStaticClass
	AffectedDirs []string
	Reason       string
}

// AnalyzeShellStatic performs deterministic shell access classification.
// It returns unknown when the command cannot be proven read-only or tied to
// concrete write targets; callers can then fall back to slower classifiers.
func AnalyzeShellStatic(command string, posix bool) ShellStaticAnalysis {
	if posix {
		if ro, reason := IsReadOnlyPOSIX(command); ro {
			return ShellStaticAnalysis{Class: ShellStaticRead, Reason: reason}
		}
		return analyzeShellStaticWrite(command, posix)
	}

	if ro, reason, paths := IsReadOnlyPowerShell(command); ro {
		return ShellStaticAnalysis{
			Class:        ShellStaticRead,
			AffectedDirs: paths,
			Reason:       reason,
		}
	}
	return analyzeShellStaticWrite(command, posix)
}

func analyzeShellStaticWrite(command string, posix bool) ShellStaticAnalysis {
	dirs := ExtractWritableDirs(command, posix)
	if len(dirs) == 0 {
		return ShellStaticAnalysis{Class: ShellStaticUnknown}
	}
	return ShellStaticAnalysis{
		Class:        ShellStaticWrite,
		AffectedDirs: dirs,
		Reason:       "write command (static analysis)",
	}
}
