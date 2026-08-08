//go:build windows

package tools

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// evalPlatformFinalPath resolves Windows reparse points, including directory
// junctions. filepath.EvalSymlinks does not reliably expose the junction target
// on every supported Windows build, while the kernel's final handle path does.
func evalPlatformFinalPath(path string) (string, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", fmt.Errorf("open path for final-name resolution: %w", err)
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 512)
	for {
		n, finalErr := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if finalErr != nil {
			if n > uint32(len(buffer)) {
				buffer = make([]uint16, n+1)
				continue
			}
			return "", fmt.Errorf("resolve final path name: %w", finalErr)
		}
		resolved := string(utf16.Decode(buffer[:n]))
		switch {
		case strings.HasPrefix(resolved, `\\?\UNC\`):
			return `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`), nil
		case strings.HasPrefix(resolved, `\\?\Volume{`):
			// Mounted-volume device paths have no drive-letter form to shorten
			// to. Keep the verbatim form; comparisons against drive-letter
			// approvals will not match, which fails closed rather than
			// authorizing a different path.
			return resolved, nil
		case strings.HasPrefix(resolved, `\\?\`):
			return strings.TrimPrefix(resolved, `\\?\`), nil
		default:
			return resolved, nil
		}
	}
}
