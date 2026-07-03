package approval

import (
	"sort"
	"strings"

	"github.com/panjie/mods/internal/pathutil"
)

// Path helpers used by the writable-directory extractor and the
// dirWithinPaths predicate. They are pure functions over string forms
// and have no dependencies on shell parsing or rule types.

func destinationDir(path string) string {
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		return cleanDir(path)
	}
	return parentDir(path)
}

func parentDir(path string) string {
	return pathutil.ParentDir(path)
}

func cleanDir(path string) string {
	cleaned := pathutil.NormalizePath(path, pathutil.DefaultOptions("", pathutil.FlavorPOSIX))
	if cleaned == "" {
		return "."
	}
	return cleaned
}

func dedupeSorted(items []string) []string {
	sort.Strings(items)
	result := items[:0]
	for i, item := range items {
		if i == 0 || items[i-1] != item {
			result = append(result, item)
		}
	}
	return result
}
