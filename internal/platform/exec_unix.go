//go:build !windows

package platform

import "os/exec"

func HideCommandWindow(_ *exec.Cmd) {}
