package image

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDetectMimeType(t *testing.T) {
	t.Run("png", func(t *testing.T) {
		mime, err := DetectMimeType(pngHeader())
		require.NoError(t, err)
		require.Equal(t, "image/png", mime)
	})

	t.Run("jpg", func(t *testing.T) {
		mime, err := DetectMimeType(jpgHeader())
		require.NoError(t, err)
		require.Equal(t, "image/jpeg", mime)
	})

	t.Run("gif", func(t *testing.T) {
		mime, err := DetectMimeType(gifHeader())
		require.NoError(t, err)
		require.Equal(t, "image/gif", mime)
	})

	t.Run("webp", func(t *testing.T) {
		mime, err := DetectMimeType(webpHeader())
		require.NoError(t, err)
		require.Equal(t, "image/webp", mime)
	})

	t.Run("empty", func(t *testing.T) {
		_, err := DetectMimeType(nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "empty")
	})

	t.Run("unsupported", func(t *testing.T) {
		_, err := DetectMimeType([]byte{0x00, 0x00, 0x00})
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported")
	})
}

func TestReadImage(t *testing.T) {
	t.Run("valid png file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "test.png")
		require.NoError(t, os.WriteFile(path, pngHeader(), 0o644))
		data, mime, err := ReadImage(path)
		require.NoError(t, err)
		require.Equal(t, "image/png", mime)
		require.Equal(t, pngHeader(), data)
	})

	t.Run("not found", func(t *testing.T) {
		_, _, err := ReadImage("/nonexistent/file.png")
		require.Error(t, err)
		require.Contains(t, err.Error(), "not found")
	})

	t.Run("unsupported format", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.bin")
		require.NoError(t, os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o644))
		_, _, err := ReadImage(path)
		require.Error(t, err)
		require.Contains(t, err.Error(), "unsupported")
	})
}

func pngHeader() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}
}

func jpgHeader() []byte {
	return []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 0x4A, 0x46, 0x49, 0x46, 0x00}
}

func gifHeader() []byte {
	return []byte{0x47, 0x49, 0x46, 0x38, 0x39, 0x61, 0x01, 0x00, 0x01, 0x00}
}

func webpHeader() []byte {
	return []byte{0x52, 0x49, 0x46, 0x46, 0x10, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50, 0x56, 0x50, 0x38, 0x20}
}
