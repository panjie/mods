//go:build windows

package tools

import "golang.org/x/sys/windows"

func testProcessExists(pid int) bool {
	process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	_ = windows.CloseHandle(process)
	return true
}
