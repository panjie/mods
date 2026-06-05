//go:build linux

package clipboard

import (
	"fmt"
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
	data, err := execCmd("wl-paste", "-t", "image/png")
	if err != nil || len(data) == 0 {
		return nil, fmt.Errorf("wl-copy not available")
	}
	return data, nil
}
