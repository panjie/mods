package approval

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// Target-based saved-rule matching. The application review gate combines
// ClassifyAccess with RulesAllowIntent; rule matching never reparses commands.

// RulesForDirs builds the task-scoped candidate DirAllow rule offered by the
// "Always allow" choice. The current working directory is used only to turn
// relative inputs into stable absolute paths; it is not stored as rule scope.
func RulesForDirs(dirs []string, scope Scope, mode AccessClass) []Rule {
	if len(dirs) == 0 || mode != AccessWrite {
		return nil
	}
	dirs = normalizeDirsForScope(dirs, scope)
	if len(dirs) == 0 {
		return nil
	}
	return []Rule{{
		Type:  DirAllow,
		Paths: dirs,
		Mode:  mode,
	}}
}

// RulesAllowDirs reports whether explicit write-mode, task-scoped DirAllow
// rules cover every affected directory. The supplied scope only resolves
// relative target paths. Legacy scoped write rules remain usable across a
// changed working directory, while empty-mode and read rules never authorize
// writes.
func RulesAllowDirs(rules []Rule, dirs []string, scope Scope, mode AccessClass) bool {
	if len(dirs) == 0 || mode != AccessWrite {
		return false
	}
	dirs = normalizeDirsForScope(dirs, scope)
	if len(dirs) == 0 {
		return false
	}
	var allowedPaths []string
	for _, rule := range rules {
		if rule.Type != DirAllow {
			continue
		}
		if rule.Mode != AccessWrite {
			continue
		}
		base := scope.Value
		if rule.ScopeValue != "" {
			// Pre-task-scope builds could persist relative paths together with
			// their cwd. Resolve those paths against the original value,
			// then ignore the scope for authorization matching.
			base = rule.ScopeValue
		}
		allowedPaths = append(allowedPaths, normalizeShellDirsForWorkingDir(rule.Paths, base)...)
	}
	for _, dir := range dirs {
		if !dirWithinPaths(allowedPaths, dir) {
			return false
		}
	}
	return len(allowedPaths) > 0
}

// RulesForRemoteOrigins builds a task-scoped remote-write allow rule. The
// RuleSet itself belongs to one saved session, and origins remain stable if
// that session is continued from another working directory.
func RulesForRemoteOrigins(origins []string) []Rule {
	origins = NormalizeRemoteOrigins(origins)
	if len(origins) == 0 {
		return nil
	}
	return []Rule{{Type: RemoteAllow, Origins: origins, Mode: AccessWrite}}
}

func RulesAllowRemoteOrigins(rules []Rule, origins []string) bool {
	origins = NormalizeRemoteOrigins(origins)
	if len(origins) == 0 {
		return false
	}
	var allowed []string
	for _, rule := range rules {
		if rule.Type != RemoteAllow || rule.Mode != AccessWrite {
			continue
		}
		allowed = append(allowed, NormalizeRemoteOrigins(rule.Origins)...)
	}
	for _, origin := range origins {
		if !slices.Contains(allowed, origin) {
			return false
		}
	}
	return len(allowed) > 0
}

// RulesAllowIntent reports whether saved rules cover every access group that
// still requires approval under the current policy. Groups already allowed by
// the matrix (for example a temp-directory write in auto mode) need no rule.
func RulesAllowIntent(rules []Rule, intent AccessIntent, scope Scope, safeDirs []string, reviewMode ReviewMode) bool {
	if reviewMode != ReviewAuto || intent.HasNonReusableWriteTargets() {
		return false
	}
	covered := false
	for _, group := range intent.Groups() {
		if group.Class == AccessRead {
			continue
		}
		if group.Class != AccessWrite || len(group.Dirs) == 0 && len(group.Origins) == 0 {
			return false
		}
		var reviewDirs []string
		for _, dir := range group.Dirs {
			if locateDir(dir, scope, safeDirs) != locTemp {
				reviewDirs = append(reviewDirs, dir)
			}
		}
		if len(reviewDirs) > 0 && !RulesAllowDirs(rules, reviewDirs, scope, AccessWrite) {
			return false
		}
		if len(group.Origins) > 0 && !RulesAllowRemoteOrigins(rules, group.Origins) {
			return false
		}
		covered = covered || len(reviewDirs) > 0 || len(group.Origins) > 0
	}
	return covered
}

func RulesLabel(rules []Rule) string {
	if len(rules) == 0 {
		return "this operation"
	}
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		labels = append(labels, rule.String())
	}
	return strings.Join(labels, ", ")
}

// ExtractShellCommand decodes the JSON arguments of a shell tool call
// and returns the embedded "command" string. Returns "" on any parse
// failure, which the callers treat as "do not allow".
func ExtractShellCommand(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

// dirWithinPaths reports whether target falls inside any of the
// allowed directories. It performs case-insensitive comparison for
// Windows-style paths and rejects sibling-prefix matches such as
// /tmp/cache2/file against an allowed /tmp/cache.
func dirWithinPaths(allowed []string, target string) bool {
	target = pathutil.NormalizePath(target, pathutil.DefaultOptions("", pathutil.FlavorPOSIX))
	for _, dir := range allowed {
		dir = pathutil.NormalizePath(dir, pathutil.DefaultOptions("", pathutil.FlavorPOSIX))
		if dir == "." {
			if target == "." || !pathutil.IsAbs(target) && !pathutil.IsUnresolvedHomePath(target) {
				return true
			}
			continue
		}
		if pathutil.Contains(dir, target) {
			return true
		}
	}
	return false
}

func normalizeDirsForScope(dirs []string, scope Scope) []string {
	return normalizeDirsForWorkingDir(dirs, scope.Value)
}

func normalizeDirsForWorkingDir(dirs []string, cwd string) []string {
	return pathutil.NormalizeDirs(dirs, pathutil.DefaultOptions(cwd, pathutil.FlavorPOSIX))
}

func normalizeShellDirsForWorkingDir(dirs []string, cwd string) []string {
	return pathutil.NormalizeShellDirs(dirs, pathutil.DefaultOptions(cwd, pathutil.FlavorPOSIX))
}
