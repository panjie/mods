//go:build !windows

package tools

import (
	"errors"
	"os/exec"
	"sync/atomic"
	"syscall"
	"time"
)

type processTree struct {
	pid   atomic.Int64
	grace time.Duration
}

func newProcessTree(cmd *exec.Cmd, grace time.Duration) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	tree := &processTree{grace: grace}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		pid := cmd.Process.Pid
		tree.pid.Store(int64(pid))
		err := syscall.Kill(-pid, syscall.SIGTERM)
		if errors.Is(err, syscall.ESRCH) {
			return nil
		}
		time.AfterFunc(tree.grace, func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
		return err
	}
	return tree, nil
}

func (t *processTree) Attach(cmd *exec.Cmd) error {
	if cmd.Process != nil {
		t.pid.Store(int64(cmd.Process.Pid))
	}
	return nil
}

func (t *processTree) Close() error {
	pid := int(t.pid.Load())
	if pid <= 0 {
		return nil
	}
	err := syscall.Kill(-pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
