package app

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestImageToolCallerReadsExternalPixelsWithoutReview(t *testing.T) {
	root := t.TempDir()
	external := t.TempDir()
	path := filepath.Join(external, "a.png")
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0600))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	cfg.ReviewMode = ReviewAlways
	cfg.InteractiveTTYAvailable = false
	cfg.MCPTimeout = time.Second
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterImages(registry, toolregistry.FilesystemConfig{Root: root, SafeDirs: m.safeDirs()}))
	args, _ := json.Marshal(map[string]any{"paths": []string{path}})
	result, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "read", Index: 1, Total: 1, Name: "fs_read_images", Arguments: args})
	require.NoError(t, err, result.Text)
	require.Len(t, result.Images, 1)
	require.Equal(t, b.Bytes(), result.Images[0].Data)
	require.False(t, writeTargetToolAllowed(registry, "fs_read_images"))
	require.False(t, writeTargetToolAllowed(registry, "fs_find_images"))
}
