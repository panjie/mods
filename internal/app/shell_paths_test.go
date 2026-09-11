package app

import (
	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
)

// extractExternalPaths and extractExternalPathsWithFlavor are shorthands for
// approval's literal path fallback with a default POSIX flavor and an empty
// read-only command policy. They live in the test binary on purpose: the shell
// classification tests exercise the path heuristics through them, and
// production code must not carry wrappers that only tests call.

func extractExternalPaths(command, cwdDir string) []string {
	return extractExternalPathsWithFlavor(command, cwdDir, pathutil.FlavorPOSIX)
}

func extractExternalPathsWithFlavor(command, cwdDir string, flavor pathutil.Flavor) []string {
	paths, _ := approval.ExternalShellPathFacts(command, cwdDir, flavor, approval.ReadOnlyCommandPolicy{})
	return paths
}
