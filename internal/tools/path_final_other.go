//go:build !windows

package tools

func evalPlatformFinalPath(path string) (string, error) {
	return path, nil
}
