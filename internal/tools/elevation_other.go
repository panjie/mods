//go:build !windows

package tools

import "fmt"

func (r commandRunner) runElevated() (commandRunResult, error) {
	return commandRunResult{}, fmt.Errorf("elevated is supported only on Windows; use sudo on POSIX systems")
}

func RunElevationHelper([]string) error {
	return fmt.Errorf("Windows UAC is unavailable on this platform")
}
