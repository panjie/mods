// Package clipboard provides cross-platform clipboard image reading.
package clipboard

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

var ErrNoImage = errors.New("no image found in clipboard")

// ReadImage reads an image from the system clipboard.
// Returns raw bytes, MIME type, and error.
func ReadImage() ([]byte, string, error) {
	data, err := readImage()
	if err != nil {
		return nil, "", err
	}
	return data, "image/png", nil
}

func execCmd(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}

func tempDir() string {
	return os.TempDir()
}
