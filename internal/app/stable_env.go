package app

import (
	"regexp"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// powerShellEnvMutationPatterns detect command text that reassigns or removes
// environment variables. Static stable-env expansion must be suppressed for
// such commands because the classifier would resolve the pre-assignment value
// while the child shell observes the mutated one. The Env: drive pattern below
// matches only bare Env: tokens ([^$]env:): a $env:NAME reference inside an
// argument reads the variable (for example Set-Content -Path "$env:TEMP\log")
// and must not be mistaken for a mutation of it.
var powerShellEnvMutationPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\$\{?env:[a-z0-9_.()]+\}?\s*[-+*/]?=`),
	regexp.MustCompile(`(?i)\b(?:set|new|remove|copy|move|clear)-(?:item|content|variable)\b[^;|\r\n]*[^$]env:`),
	regexp.MustCompile(`(?i)\[\s*(?:system\.)?environment\s*\]\s*::\s*setenvironmentvariable\b`),
}

func commandMutatesPowerShellEnvironment(command string) bool {
	for _, pattern := range powerShellEnvMutationPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// resolvePowerShellEnvTargets materializes dynamic targets that reference
// environment variables whose inherited values are statically known and
// identical inside the child shell. Three kinds resolve:
//
//   - path-shaped references ($env:NAME\tail) expand to the joined path,
//   - bare references with at least one value-shaped textual use expand to
//     the variable's directory value (reads only; write value-uses stay
//     dynamic so the corrective loop keeps applying),
//   - bare references whose uses are all path-shaped are subsumed: every
//     textual reference expands to a concrete path instead.
//
// A path-shaped token that cannot expand as one clean path (for example a
// flattened PowerShell array argument mixing quotes and further references)
// falls back to the textual-use scan.
//
// References under a name shadowed by this call's secret environment never
// expand: the child shell would observe the secret's value.
func resolvePowerShellEnvTargets(known, dynamic []string, workspace, command string, shadowedEnv map[string]bool, allowValueDirs bool) ([]string, []string) {
	if len(dynamic) == 0 {
		return known, dynamic
	}
	opts := pathutil.DefaultOptions(workspace, pathutil.FlavorPowerShell)
	kept := make([]string, 0, len(dynamic))
	var expanded []string
	for _, target := range dynamic {
		trimmed := strings.TrimSpace(target)
		name, pathShaped, ok := pathutil.EnvRefParts(trimmed, pathutil.FlavorPowerShell)
		if !ok || shadowedEnv[strings.ToUpper(name)] {
			kept = append(kept, target)
			continue
		}
		if pathShaped {
			if resolved, expandable := pathutil.ExpandEnvPath(trimmed, opts); expandable {
				expanded = appendMissingShellDirs(expanded, []string{resolved})
				continue
			}
			// A token whose tail mixes quoting levels or further references —
			// a flattened PowerShell array argument — cannot expand as one
			// path; its textual uses still bound the variable soundly, so the
			// same scan that subsumes bare references resolves it.
			if dirs, resolved := bareEnvRefDirs(name, command, opts, allowValueDirs); resolved {
				expanded = appendMissingShellDirs(expanded, dirs)
				continue
			}
			kept = append(kept, target)
			continue
		}
		if dirs, resolved := bareEnvRefDirs(name, command, opts, allowValueDirs); resolved {
			expanded = appendMissingShellDirs(expanded, dirs)
			continue
		}
		kept = append(kept, target)
	}
	if len(expanded) == 0 && len(kept) == len(dynamic) {
		return known, dynamic
	}
	return appendMissingShellDirs(known, expanded), kept
}

// bareEnvRefDirs decides a bare environment-variable reference from its
// textual uses: value-shaped uses make the variable's directory value the
// honest scope; otherwise every path-shaped use expands to a concrete path
// (subsumption). A well-known public variable (PATH, OS, ...) carries no
// capability, so for reads its reference is dropped outright with nothing
// to review. It reports false when the uses cannot be bounded soundly,
// leaving the reference dynamic.
func bareEnvRefDirs(name, command string, opts pathutil.Options, allowValueDir bool) ([]string, bool) {
	if allowValueDir && pathutil.IsPublicEnvName(name, opts.Flavor) {
		return nil, true
	}
	uses, ok := pathutil.ExpandEnvRefs(command, name, opts)
	if !ok {
		return nil, false
	}
	if uses.Bare > 0 {
		if !allowValueDir {
			return nil, false
		}
		value, valid := pathutil.EnvDirValue(name, opts)
		if !valid {
			return nil, false
		}
		return []string{value}, true
	}
	if len(uses.Paths) > 0 {
		return uses.Paths, true
	}
	return nil, false
}
