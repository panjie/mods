package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	ctx := context.Background()
	const content = "just text"
	t.Run("normal msg", func(t *testing.T) {
		msg, err := loadMsg(ctx, content)
		require.NoError(t, err)
		require.Equal(t, content, msg)
	})

	t.Run("file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "foo.txt")
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))

		msg, err := loadMsg(ctx, "file://"+path)
		require.NoError(t, err)
		require.Equal(t, content, msg)
	})

	t.Run("http url", func(t *testing.T) {
		msg, err := loadMsg(ctx, "http://raw.githubusercontent.com/charmbracelet/mods/main/LICENSE")
		require.NoError(t, err)
		require.Contains(t, msg, "MIT License")
	})

	t.Run("https url", func(t *testing.T) {
		msg, err := loadMsg(ctx, "https://raw.githubusercontent.com/charmbracelet/mods/main/LICENSE")
		require.NoError(t, err)
		require.Contains(t, msg, "MIT License")
	})
}
