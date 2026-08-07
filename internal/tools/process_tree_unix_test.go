//go:build !windows

package tools

import (
	"errors"
	"syscall"
)

func testProcessExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
