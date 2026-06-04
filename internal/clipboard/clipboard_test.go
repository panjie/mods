package clipboard

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContainsImageTarget(t *testing.T) {
	t.Run("png found", func(t *testing.T) {
		require.True(t, containsImageTarget("TARGETS\nimage/png\nimage/jpeg"))
	})
	t.Run("gif found", func(t *testing.T) {
		require.True(t, containsImageTarget("TARGETS\nimage/gif"))
	})
	t.Run("no image", func(t *testing.T) {
		require.False(t, containsImageTarget("TARGETS\ntext/plain\nUTF8_STRING"))
	})
	t.Run("empty", func(t *testing.T) {
		require.False(t, containsImageTarget(""))
	})
}

func TestIsImageData(t *testing.T) {
	t.Run("png magic bytes", func(t *testing.T) {
		require.True(t, isImageData([]byte{0x89, 0x50, 0x4E, 0x47, 0x00, 0x00}))
	})
	t.Run("jpg magic bytes", func(t *testing.T) {
		require.True(t, isImageData([]byte{0xFF, 0xD8, 0xFF, 0xE0}))
	})
	t.Run("gif magic bytes", func(t *testing.T) {
		require.True(t, isImageData([]byte{0x47, 0x49, 0x46, 0x38}))
	})
	t.Run("webp magic bytes", func(t *testing.T) {
		data := []byte{0x52, 0x49, 0x46, 0x46, 0x00, 0x00, 0x00, 0x00, 0x57, 0x45, 0x42, 0x50}
		require.True(t, isImageData(data))
	})
	t.Run("too short", func(t *testing.T) {
		require.False(t, isImageData([]byte{0x89}))
	})
	t.Run("random data", func(t *testing.T) {
		require.False(t, isImageData([]byte{0x00, 0x01, 0x02, 0x03}))
	})
}

func TestTempDir(t *testing.T) {
	require.NotEmpty(t, tempDir())
}
