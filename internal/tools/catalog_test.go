package tools

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCatalogNeedsNoExistingDirectoryAndCannotExecute(t *testing.T) {
	registry := NewCatalogRegistry()
	defer registry.Close()
	cfg := FilesystemConfig{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	require.NoError(t, RegisterFilesystem(registry, cfg))
	require.NoError(t, RegisterImages(registry, cfg))
	require.NoError(t, RegisterMedia(registry, cfg))
	require.NoError(t, RegisterDownload(registry, cfg))
	for _, spec := range registry.Specs() {
		_, err := registry.Call(context.Background(), spec.Name, []byte(`{}`))
		require.ErrorContains(t, err, "catalogue tools cannot execute")
		_, err = registry.CallResult(context.Background(), spec.Name, []byte(`{}`))
		require.ErrorContains(t, err, "catalogue tools cannot execute")
	}
	require.NoDirExists(t, cfg.Root)
}
