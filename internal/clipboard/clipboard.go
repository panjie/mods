// Package clipboard provides cross-platform clipboard image reading.
package clipboard

import (
	"errors"
	"os"
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
