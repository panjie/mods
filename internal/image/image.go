// Package image provides utilities for handling image files in mods.
package image

import (
	"bytes"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"os"

	_ "golang.org/x/image/webp"
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
	info, err := os.Stat(path)
	if err != nil {
		return nil, "", fmt.Errorf("image file not found or inaccessible: %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("image is not a regular file: %s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", fmt.Errorf("image file not found: %s", path)
		}
		return nil, "", fmt.Errorf("reading image file %s: %w", path, err)
	}
	defer file.Close() //nolint:errcheck
	data, err := io.ReadAll(io.LimitReader(file, MaxTotalImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("reading image file %s: %w", path, err)
	}
	if len(data) > MaxTotalImageBytes {
		return nil, "", fmt.Errorf("image file exceeds limit of %d bytes: %s", MaxTotalImageBytes, path)
	}
	mime, err := DetectMimeType(data)
	if err != nil {
		return nil, "", err
	}
	return data, mime, nil
}

// MaxImageCount bounds cumulative image inputs, including conversation history.
const MaxImageCount = 64

// ValidateImage rejects malformed images and unreasonable decoded dimensions.
func ValidateImage(data []byte) error {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("invalid image: %w", err)
	}
	if config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 40_000_000 {
		return fmt.Errorf("image dimensions exceed 40 million pixels")
	}
	if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
		return fmt.Errorf("invalid image pixels: %w", err)
	}
	return nil
}
