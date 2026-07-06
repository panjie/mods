package approval

import "github.com/panjie/mods/internal/pathutil"

// ReviewMode mirrors the config-layer review modes without importing the
// config package, keeping approval free of that dependency.
type ReviewMode string

const (
	ReviewNever  ReviewMode = "never"
	ReviewAuto   ReviewMode = "auto"
	ReviewAlways ReviewMode = "always"
)

// AccessClass describes whether a tool invocation reads or writes.
type AccessClass string

const (
	AccessRead  AccessClass = "read"
	AccessWrite AccessClass = "write"
)

// AccessIntent is the unified, tool-neutral description of what a tool
// invocation touches: an access class plus the absolute directories it
// operates on. It is the sole input to the approval matrix.
type AccessIntent struct {
	Class  AccessClass
	Dirs   []string
	Reason string
}

// Decision is the outcome of the approval matrix.
type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
)

type dirLocation int

const (
	locUnknown dirLocation = iota
	locWorkspace
	locTemp
	locExternal
)

// locateDir classifies an absolute path against the workspace scope and
// safe directories. Relative paths are treated as workspace-local because
// fs_* tools resolve them against the workspace before reaching here
// and shell commands without absolute paths stay inside the workspace.
func locateDir(path string, scope Scope, safeDirs []string) dirLocation {
	switch pathutil.Location(path, scope.Value, safeDirs) {
	case pathutil.LocationUnknown:
		return locUnknown
	case pathutil.LocationWorkspace:
		return locWorkspace
	case pathutil.LocationSafe:
		return locTemp
	default:
		return locExternal
	}
}

// ClassifyAccess applies the directory-centric approval matrix.
//
//	default auto mode:
//	  workspace: read=allow, write=ask
//	  temp dir:  read=allow, write=allow
//	  external:  read=ask,   write=ask
//
// ReviewNever forces allow. Empty Dirs degrades to read=allow / write=ask.
func ClassifyAccess(intent AccessIntent, scope Scope, safeDirs []string, mode ReviewMode) Decision {
	if mode == ReviewNever {
		return DecisionAllow
	}
	if len(intent.Dirs) == 0 {
		if intent.Class == AccessRead {
			return DecisionAllow
		}
		return DecisionAsk
	}
	for _, d := range intent.Dirs {
		switch locateDir(d, scope, safeDirs) {
		case locExternal:
			return DecisionAsk
		case locWorkspace:
			if intent.Class == AccessWrite {
				return DecisionAsk
			}
		case locTemp:
			// matrix allow cell, keep scanning
		default:
			return DecisionAsk
		}
	}
	return DecisionAllow
}

// ExternalDirs returns the subset of intent.Dirs that fall outside the
// workspace and outside any safe directory. Callers inject these into the
// tool-call context so resolveWorkspacePath can honor approval.
func ExternalDirs(intent AccessIntent, scope Scope, safeDirs []string) []string {
	var out []string
	for _, d := range intent.Dirs {
		if locateDir(d, scope, safeDirs) == locExternal {
			out = append(out, d)
		}
	}
	return out
}
