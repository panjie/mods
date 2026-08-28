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
	regexp.MustCompile(`(?i)\[environment\]::setenvironmentvariable`),
}

func commandMutatesPowerShellEnvironment(command string) bool {
	for _, pattern := range powerShellEnvMutationPatterns {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

// resolveStableEnvTargets moves path-shaped stable-environment expressions
// (for example $env:SystemRoot\WinSxS) from the dynamic target list into
// concrete known directories. Bare value references whose information is
// fully covered by an expanded sibling expression are dropped; every other
// dynamic target is kept unchanged.
func resolveStableEnvTargets(known, dynamic []string, workspace string) ([]string, []string) {
	if len(dynamic) == 0 {
		return known, dynamic
	}
	opts := pathutil.DefaultOptions(workspace, pathutil.FlavorPowerShell)
	kept := make([]string, 0, len(dynamic))
	var expanded, expandedRefs []string
	for _, target := range dynamic {
		trimmed := strings.TrimSpace(target)
		if resolved, ok := pathutil.ExpandStableEnvPath(trimmed, opts); ok {
			expanded = appendMissingShellDirs(expanded, []string{resolved})
			expandedRefs = append(expandedRefs, trimmed)
			continue
		}
		kept = append(kept, target)
	}
	kept = dropSubsumedStableEnvRefs(kept, expandedRefs)
	if len(expanded) == 0 {
		return known, dynamic
	}
	return appendMissingShellDirs(known, expanded), kept
}

func dropSubsumedStableEnvRefs(kept, expandedRefs []string) []string {
	if len(expandedRefs) == 0 {
		return kept
	}
	result := make([]string, 0, len(kept))
	for _, target := range kept {
		trimmed := strings.TrimSpace(target)
		if stableEnvValueReference(trimmed) && stableEnvRefSubsumed(trimmed, expandedRefs) {
			continue
		}
		result = append(result, target)
	}
	return result
}

func stableEnvValueReference(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	if strings.HasPrefix(lower, "$env:") {
		return pathutil.IsStableEnvName(lower[len("$env:"):])
	}
	if strings.HasPrefix(lower, "${env:") && strings.HasSuffix(lower, "}") {
		return pathutil.IsStableEnvName(lower[len("${env:") : len(lower)-1])
	}
	return false
}

func stableEnvRefSubsumed(ref string, expandedRefs []string) bool {
	for _, expanded := range expandedRefs {
		if len(expanded) <= len(ref) {
			continue
		}
		if !strings.EqualFold(expanded[:len(ref)], ref) {
			continue
		}
		next := expanded[len(ref)]
		if next == '/' || next == '\\' {
			return true
		}
	}
	return false
}
