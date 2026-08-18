//go:build !windows

package pathutil

func resolvePlatformFinalPath(path string) (string, error) {
	return path, nil
}
