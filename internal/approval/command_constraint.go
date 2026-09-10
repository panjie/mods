package approval

import (
	"path"
	"strings"
)

// RequiresSimplification is independent of effect inference and approval rules.
// In particular, a classifier's read verdict cannot clear opaque syntax.
func (a CommandAssessment) RequiresSimplification() bool {
	if a.StaticRead && a.Effect == EffectRead {
		return false
	}
	if a.Shape.Opaque || a.Reviewability.Level == ReviewabilityOpaque || a.Shape.TopLevelActions > 1 {
		return true
	}
	if a.Effect != EffectRead && (len(a.DynamicTargets) > 0 || len(a.UnresolvedRemoteTargets) > 0) {
		return true
	}
	for _, reason := range a.Reviewability.Reasons {
		if reason == ReviewabilityMixedReadWrite || reason == ReviewabilityDynamicWriteTarget || reason == ReviewabilityNestedShellHost {
			return true
		}
	}
	return false
}

// executableCarriesScript identifies common code hosts, not arbitrary program
// semantics. Unknown executables still require ordinary effect/target review.
// Keep this bounded: wrappers cannot cause unbounded recursive analysis.
func executableCarriesScript(program string, args []string, depth int) bool {
	if depth > 4 {
		return true
	}
	name := strings.ToLower(path.Base(strings.ReplaceAll(program, `\`, "/")))
	name = strings.TrimSuffix(name, ".exe")
	if name == "env" || name == "sudo" || name == "command" || name == "nohup" {
		for i := 0; i < len(args); i++ {
			arg := args[i]
			if strings.HasPrefix(arg, "-") {
				switch arg {
				case "--", "-i", "--ignore-environment", "-n", "-E", "-H", "-k", "-K", "-p":
					// -p takes a value in sudo, but is a flag in command.
					if arg == "-p" && name == "sudo" {
						i++
					}
				case "-u", "--unset", "--user", "-g", "--group", "-h", "--host", "-C", "--chdir":
					i++
				default:
					// Split strings, unknown option arity, or alternate execution
					// modes require full review, not guessed unwrapping.
					return true
				}
				continue
			}
			if strings.Contains(arg, "=") {
				continue
			}
			return executableCarriesScript(arg, args[i+1:], depth+1)
		}
	}
	if name == "xargs" {
		return true
	}
	if name == "find" {
		for i, arg := range args {
			if arg == "-exec" || arg == "-execdir" || arg == "-ok" || arg == "-okdir" {
				if i+1 >= len(args) {
					return true
				}
				if executableCarriesScript(args[i+1], args[i+2:], depth+1) {
					return true
				}
			}
		}
	}
	for _, suffix := range []string{".sh", ".bash", ".zsh", ".ps1", ".bat", ".cmd", ".py", ".js", ".mjs", ".cjs", ".rb", ".pl"} {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	if len(args) == 1 && (args[0] == "--version" || args[0] == "-V" || args[0] == "--help") {
		return false
	}
	if shellHostPrograms[name] || name == "eval" || name == "exec" || name == "." || name == "source" || name == "invoke-expression" || name == "iex" {
		return true
	}
	if strings.HasPrefix(name, "python") || name == "py" || name == "node" || name == "nodejs" || name == "ruby" || name == "perl" || name == "lua" || name == "osascript" || name == "cscript" || name == "wscript" {
		return true
	}
	if name == "emacs" || name == "emacsclient" || name == "vim" || name == "nvim" {
		for _, arg := range args {
			if arg == "--eval" || strings.HasPrefix(arg, "--eval=") || arg == "-e" || arg == "-l" || arg == "--load" || arg == "-c" || arg == "-S" || strings.HasPrefix(arg, "+") {
				return true
			}
		}
	}
	return false
}
