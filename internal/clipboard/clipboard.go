// Package clipboard provides cross-platform clipboard image reading.
package clipboard

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/panjie/mods/internal/platform"
)

var ErrNoImage = errors.New("no image found in clipboard")

func ReadImage() ([]byte, string, error) {
	data, err := readImage()
	if err != nil {
		return nil, "", err
	}
	return data, "image/png", nil
}

func tempDir() string {
	return os.TempDir()
}

func containsImageTarget(targets string) bool {
	for _, t := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		if strings.Contains(targets, t) {
			return true
		}
	}
	return false
}

func isImageData(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	if bytes.HasPrefix(data, []byte{0x89, 0x50, 0x4E, 0x47}) {
		return true
	}
	if bytes.HasPrefix(data, []byte{0xFF, 0xD8, 0xFF}) {
		return true
	}
	if bytes.HasPrefix(data, []byte{0x47, 0x49, 0x46, 0x38}) {
		return true
	}
	if len(data) >= 12 &&
		bytes.HasPrefix(data, []byte{0x52, 0x49, 0x46, 0x46}) &&
		bytes.HasPrefix(data[8:], []byte{0x57, 0x45, 0x42, 0x50}) {
		return true
	}
	return false
}

// execCmd runs an external command with the parent environment and returns
// its stdout. On Windows the spawned console window is hidden via
// platform.HideCommandWindow (no-op on unix), so clipboard helpers do not
// need to duplicate that logic per-platform.
func execCmd(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Env = os.Environ()
	platform.HideCommandWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("%s: %s", err, string(exitErr.Stderr))
		}
		return nil, err
	}
	return out, nil
}
