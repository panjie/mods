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
// invocation touches. Class/Dirs is the compact representation for a
// single-mode operation. ReadDirs/WriteDirs represent mixed operations such
// as copying from an external source into a writable destination. A non-nil
// split slice declares that mode even when its directory set is unknown.
type AccessIntent struct {
	Class     AccessClass
	Dirs      []string
	ReadDirs  []string
	WriteDirs []string
	Reason    string
}

type AccessGroup struct {
	Class AccessClass
	Dirs  []string
}

func (intent AccessIntent) Groups() []AccessGroup {
	if intent.ReadDirs != nil || intent.WriteDirs != nil {
		groups := make([]AccessGroup, 0, 2)
		if intent.ReadDirs != nil {
			groups = append(groups, AccessGroup{Class: AccessRead, Dirs: intent.ReadDirs})
		}
		if intent.WriteDirs != nil {
			groups = append(groups, AccessGroup{Class: AccessWrite, Dirs: intent.WriteDirs})
		}
		return groups
	}
	if intent.Class == "" {
		return nil
	}
	return []AccessGroup{{Class: intent.Class, Dirs: intent.Dirs}}
}

func (intent AccessIntent) HasAccess() bool {
	return len(intent.Groups()) > 0
}

func (intent AccessIntent) DominantClass() AccessClass {
	groups := intent.Groups()
	for _, group := range groups {
		if group.Class == AccessWrite {
			return AccessWrite
		}
	}
	if len(groups) > 0 {
		return groups[0].Class
	}
	return ""
}

func (intent AccessIntent) AllDirs() []string {
	seen := map[string]struct{}{}
	var dirs []string
	for _, group := range intent.Groups() {
		for _, dir := range group.Dirs {
			if _, ok := seen[dir]; ok {
				continue
			}
			seen[dir] = struct{}{}
			dirs = append(dirs, dir)
		}
	}
	return dirs
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
	groups := intent.Groups()
	if len(groups) == 0 {
		return DecisionAsk
	}
	for _, group := range groups {
		if len(group.Dirs) == 0 {
			if group.Class == AccessWrite {
				return DecisionAsk
			}
			continue
		}
		for _, d := range group.Dirs {
			switch locateDir(d, scope, safeDirs) {
			case locExternal:
				return DecisionAsk
			case locWorkspace:
				if group.Class == AccessWrite {
					return DecisionAsk
				}
			case locTemp:
				// matrix allow cell, keep scanning
			default:
				return DecisionAsk
			}
		}
	}
	return DecisionAllow
}

// ExternalDirs returns the subset of all read and write directories that fall
// outside the workspace and outside any safe directory. Callers inject these
// into the tool-call context so resolveWorkspacePath can honor approval.
func ExternalDirs(intent AccessIntent, scope Scope, safeDirs []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, group := range intent.Groups() {
		for _, d := range group.Dirs {
			if locateDir(d, scope, safeDirs) != locExternal {
				continue
			}
			if _, ok := seen[d]; ok {
				continue
			}
			seen[d] = struct{}{}
			out = append(out, d)
		}
	}
	return out
}
