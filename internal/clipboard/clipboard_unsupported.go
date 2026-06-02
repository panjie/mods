//go:build !linux && !windows && !darwin

package clipboard

import "fmt"

func readImage() ([]byte, error) {
	return nil, fmt.Errorf("clipboard image reading not supported on this platform (requires macOS, Linux, or Windows)")
}
