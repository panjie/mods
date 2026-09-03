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
// invocation touches. Class/Dirs/RemoteOrigins is the compact representation
// for a single-mode operation. The split fields represent mixed operations
// such as copying from an external source into a writable destination. A
// non-nil split slice declares that mode even when its target set is unknown.
// UnresolvedPaths records runtime expressions that cannot safely become
// concrete directory rules (for example $PROFILE or $target).
type AccessIntent struct {
	Class           AccessClass
	Dirs            []string
	RemoteOrigins   []string
	ReadDirs        []string
	WriteDirs       []string
	ReadOrigins     []string
	WriteOrigins    []string
	UnresolvedPaths []string
	// UnresolvedRemoteTargets records network destinations whose origin is
	// selected at runtime. A write with an unresolved remote target can be
	// approved once, but can never produce a reusable allow rule.
	UnresolvedRemoteTargets []string
	// UncertainEffect marks a fail-closed write intent produced when command
	// analysis could not determine whether or where persistent effects occur.
	UncertainEffect bool
	DynamicProbe    bool
	Reason          string
}

type AccessGroup struct {
	Class   AccessClass
	Dirs    []string
	Origins []string
}

func (intent AccessIntent) Groups() []AccessGroup {
	if intent.ReadDirs != nil || intent.WriteDirs != nil || intent.ReadOrigins != nil || intent.WriteOrigins != nil {
		groups := make([]AccessGroup, 0, 2)
		if intent.ReadDirs != nil || intent.ReadOrigins != nil {
			groups = append(groups, AccessGroup{Class: AccessRead, Dirs: intent.ReadDirs, Origins: intent.ReadOrigins})
		}
		if intent.WriteDirs != nil || intent.WriteOrigins != nil {
			groups = append(groups, AccessGroup{Class: AccessWrite, Dirs: intent.WriteDirs, Origins: intent.WriteOrigins})
		}
		return groups
	}
	if intent.Class == "" {
		return nil
	}
	return []AccessGroup{{Class: intent.Class, Dirs: intent.Dirs, Origins: intent.RemoteOrigins}}
}

func (intent AccessIntent) HasAccess() bool {
	return len(intent.Groups()) > 0
}

func (intent AccessIntent) HasUnresolvedPaths() bool {
	return len(intent.UnresolvedPaths) > 0
}

func (intent AccessIntent) HasUnresolvedRemoteTargets() bool {
	return len(intent.UnresolvedRemoteTargets) > 0
}

func (intent AccessIntent) HasUnresolvedWriteTargets() bool {
	return intent.DominantClass() == AccessWrite &&
		(intent.UncertainEffect || intent.HasUnresolvedPaths() || intent.HasUnresolvedRemoteTargets())
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

func (intent AccessIntent) AllRemoteOrigins() []string {
	seen := map[string]struct{}{}
	var origins []string
	for _, group := range intent.Groups() {
		for _, origin := range group.Origins {
			if _, ok := seen[origin]; ok {
				continue
			}
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	return origins
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

// locateDir classifies a path against the tool working directory and safe
// directories. This location is used for runtime path capabilities and the
// temporary-directory exception, never as an approval-rule boundary. Relative
// paths are resolved against the tool working directory before execution.
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

// ClassifyAccess applies the write-only approval matrix. Reads are always
// allowed, irrespective of location, dynamic targets, or review mode. Writes
// ask unless every target is a safe temporary directory. ReviewNever forces
// allow. Empty or unresolved write targets fail closed to ask.
func ClassifyAccess(intent AccessIntent, scope Scope, safeDirs []string, mode ReviewMode) Decision {
	if mode == ReviewNever {
		return DecisionAllow
	}
	groups := intent.Groups()
	if len(groups) == 0 {
		return DecisionAsk
	}
	for _, group := range groups {
		if group.Class == AccessRead {
			continue
		}
		if group.Class != AccessWrite {
			return DecisionAsk
		}
		if intent.UncertainEffect || intent.HasUnresolvedPaths() || intent.HasUnresolvedRemoteTargets() {
			return DecisionAsk
		}
		if len(group.Dirs) == 0 && len(group.Origins) == 0 {
			return DecisionAsk
		}
		if len(group.Origins) > 0 {
			return DecisionAsk
		}
		for _, d := range group.Dirs {
			switch locateDir(d, scope, safeDirs) {
			case locExternal, locWorkspace, locUnknown:
				return DecisionAsk
			case locTemp:
				// Safe temporary writes never require review.
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
