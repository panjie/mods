//go:build !windows

package watch

import "os"

func openWatchFile(path string) (*os.File, error) {
	return os.Open(path)
}
