package approval

import (
	"os"
	"runtime"
)

// SafeDirs returns the filesystem directories that may be accessed without
// explicit approval. Both the approval matrix and filesystem tool enforcement
// must consume this function so a path cannot be approved by one layer and
// rejected by the other.
func SafeDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		// On macOS os.TempDir() is a per-user /var/folders path, while /tmp is
		// the conventional scratch location models and users commonly choose.
		dirs = append(dirs, "/tmp")
	}
	return dirs
}
