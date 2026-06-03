//go:build !windows

package clipboard

import (
	"fmt"
	"os"
	"os/exec"
)

func execCmd(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}
