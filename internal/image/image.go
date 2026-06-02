// Package image provides utilities for handling image files in mods.
package image

import (
	"fmt"
	"net/http"
	"os"
)

// MaxTotalImageBytes is the maximum total size of all images in a single request.
const MaxTotalImageBytes = 20 * 1024 * 1024 // 20MB

// Supported MIME types.
var supportedMimeTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// DetectMimeType detects the MIME type of image data from its magic bytes.
func DetectMimeType(data []byte) (string, error) {
	if len(data) == 0 {
		return "", fmt.Errorf("empty image data")
	}
	mime := http.DetectContentType(data)
	if !supportedMimeTypes[mime] {
		return "", fmt.Errorf("unsupported image format: %s (supported: png, jpg, gif, webp)", mime)
	}
	return mime, nil
}

// ReadImage reads an image file from disk and returns its raw bytes and MIME type.
func ReadImage(path string) ([]byte, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("image file not found: %s", path)
		}
		return nil, "", fmt.Errorf("reading image file %s: %w", path, err)
	}
	mime, err := DetectMimeType(data)
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}
