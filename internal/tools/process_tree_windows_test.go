//go:build windows

package tools

import (
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
)

func testProcessExists(pid int) bool {
	process, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(process)
	status, err := windows.WaitForSingleObject(process, 0)
	return err != nil || status != windows.WAIT_OBJECT_0
}

func TestProcessExistsIgnoresExitedProcessWithOpenHandle(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestProcessHelperProcess$", "--", "argv")
	platformPrepareManagedCommand(cmd)
	require.NoError(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(cmd.Process.Pid))
	require.NoError(t, err)
	defer windows.CloseHandle(handle)
	require.NoError(t, cmd.Wait())
	require.False(t, testProcessExists(cmd.Process.Pid))
}
