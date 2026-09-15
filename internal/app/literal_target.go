package app

import (
	"strings"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
)

// propagateLiteralTargets materializes dynamic targets that reference a
// variable with a statically known literal value (from the assessment's
// LiteralAssignments) into concrete directories. Resolved targets are dropped
// from the dynamic list and their normalized paths appended to known,
// mirroring resolvePowerShellEnvTargets so downstream parent-directory
// normalization and rule generation see concrete paths. Non-filesystem
// PowerShell provider values stay separate so they cannot become DirAllow
// rules.
func propagateLiteralTargets(known, dynamic []string, literals map[string]string, ws string) ([]string, []string, []string) {
	if len(literals) == 0 || len(dynamic) == 0 {
		return known, dynamic, nil
	}
	opts := pathutil.DefaultOptions(ws, pathutil.FlavorPowerShell)
	kept := make([]string, 0, len(dynamic))
	var added, providers []string
	for _, target := range dynamic {
		if value, provider, ok := resolveLiteralTarget(target, literals, opts); ok {
			if provider {
				providers = appendMissingShellDirs(providers, []string{value})
				continue
			}
			added = appendMissingShellDirs(added, []string{value})
			continue
		}
		kept = append(kept, target)
	}
	if len(added) == 0 && len(providers) == 0 {
		return known, dynamic, nil
	}
	return appendMissingShellDirs(known, added), kept, providers
}

// resolveLiteralTarget resolves a single dynamic target that is a bare
// reference ($var or ${var}) optionally followed by a literal path tail, by
// substituting the variable's known literal value. The result must normalize
// to a concrete path with no runtime syntax, otherwise the target stays
// unresolved.
func resolveLiteralTarget(target string, literals map[string]string, opts pathutil.Options) (string, bool, bool) {
	target = strings.TrimSpace(target)
	for name, value := range literals {
		for _, prefix := range []string{"$" + name, "${" + name + "}"} {
			if len(target) < len(prefix) || !strings.EqualFold(target[:len(prefix)], prefix) {
				continue
			}
			rest := target[len(prefix):]
			if rest != "" && !approval.IsShellPathSeparator(rest[0]) {
				continue
			}
			providerTarget := value + rest
			if filesystemPath, ok := approval.PowerShellFilesystemProviderPath(providerTarget); ok {
				resolved := pathutil.NormalizeShellPath(filesystemPath, opts)
				if resolved != "" && !approval.IsUnresolvedShellPathExpression(resolved, false) {
					return resolved, false, true
				}
				continue
			}
			if approval.IsPowerShellProviderPath(providerTarget) {
				return providerTarget, true, true
			}
			resolved := pathutil.NormalizeShellPath(value+rest, opts)
			if resolved == "" || approval.IsUnresolvedShellPathExpression(resolved, false) {
				continue
			}
			return resolved, false, true
		}
	}
	return "", false, false
}
