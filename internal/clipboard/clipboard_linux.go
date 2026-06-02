//go:build linux

package clipboard

import (
	"bytes"
	"fmt"
	"strings"
)

func readImage() ([]byte, error) {
	if data, err := readImageWayland(); err == nil {
		return data, nil
	}
	if data, err := readImageX11(); err == nil {
		return data, nil
	}
	if data, err := readImageWLClipboard(); err == nil {
		return data, nil
	}
	return nil, fmt.Errorf("%w: xclip or wl-paste required; install one of them", ErrNoImage)
}

func readImageX11() ([]byte, error) {
	targets, err := execCmd("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o")
	if err != nil {
		return nil, fmt.Errorf("xclip not available")
	}
	if !containsImageTarget(string(targets)) {
		return nil, fmt.Errorf("%w", ErrNoImage)
	}
	for _, mime := range []string{"image/png", "image/jpeg", "image/gif", "image/webp"} {
		data, err := execCmd("xclip", "-selection", "clipboard", "-t", mime, "-o")
		if err == nil && len(data) > 0 && isImageData(data) {
			return data, nil
		}
	}
	return nil, fmt.Errorf("%w", ErrNoImage)
}

func readImageWayland() ([]byte, error) {
	data, err := execCmd("wl-paste", "-t", "image/png")
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("wl-paste not available")
	}
	return data, nil
}

func readImageWLClipboard() ([]byte, error) {
	data, err := execCmd("wl-copy", "-t", "image/png", "-o")
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("wl-copy not available")
	}
	return data, nil
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
