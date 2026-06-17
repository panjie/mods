//go:build !windows

package tools

import "os/exec"

func hideCommandWindow(_ *exec.Cmd) {}
