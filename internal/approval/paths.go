package approval

import (
	"path/filepath"
	"sort"
	"strings"
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
	path = cleanDir(path)
	if path == "" {
		return "."
	}
	if windowsStylePath(path) {
		if windowsDriveRoot(path) {
			return path
		}
		if i := strings.LastIndex(path, `\`); i >= 0 {
			if i == 0 {
				return path[:1]
			}
			if i == 2 && len(path) >= 2 && path[1] == ':' {
				return path[:i+1]
			}
			return strings.TrimRight(path[:i], `\`)
		}
		return "."
	}
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		if i == 0 {
			return path[:1]
		}
		if i == 2 && len(path) >= 2 && path[1] == ':' {
			return path[:i]
		}
		return strings.TrimRight(path[:i], `/\`)
	}
	return "."
}

func cleanDir(path string) string {
	path = strings.TrimSpace(path)
	if windowsStylePath(path) {
		return cleanWindowsPath(path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == "" {
		return "."
	}
	return cleaned
}

func descendantPrefix(path string) string {
	separator := pathSeparatorFor(path)
	if strings.HasSuffix(path, separator) {
		return path
	}
	return path + separator
}

func pathSeparatorFor(path string) string {
	if strings.Contains(path, "\\") {
		return "\\"
	}
	return "/"
}

func windowsPathIsAbs(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func windowsStylePath(path string) bool {
	return strings.Contains(path, `\`) || windowsPathHasDrive(path)
}

func windowsPathHasDrive(path string) bool {
	return len(path) >= 2 && path[1] == ':'
}

func windowsDriveRoot(path string) bool {
	return len(path) == 3 && path[1] == ':' && path[2] == '\\'
}

func cleanWindowsPath(path string) string {
	path = strings.ReplaceAll(path, "/", `\`)
	if path == "" {
		return "."
	}
	for len(path) > 1 && strings.HasSuffix(path, `\`) && !windowsDriveRoot(path) {
		path = strings.TrimSuffix(path, `\`)
	}
	return path
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
