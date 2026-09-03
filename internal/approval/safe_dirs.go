package approval

import (
	"os"
	"runtime"
)

// SafeDirs returns scratch directories that may be written without explicit
// approval. Filesystem enforcement also consumes this set so the execution
// boundary agrees with the approval matrix.
func SafeDirs() []string {
	dirs := []string{os.TempDir()}
	if runtime.GOOS != "windows" {
		// On macOS os.TempDir() is a per-user /var/folders path, while /tmp is
		// the conventional scratch location models and users commonly choose.
		dirs = append(dirs, "/tmp")
	}
	return dirs
}
