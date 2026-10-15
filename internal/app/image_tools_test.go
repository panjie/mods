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
	"github.com/panjie/mods/internal/testutil/mediafixture"
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
	require.Len(t, result.Media, 1)
	require.Equal(t, b.Bytes(), result.Media[0].Data)
	require.False(t, writeTargetToolAllowed(registry, "fs_read_images"))
	require.False(t, writeTargetToolAllowed(registry, "fs_find_images"))
}

func TestAudioToolCallerReadsExternalRecordingWithoutReview(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	path := filepath.Join(external, "录音.wav")
	require.NoError(t, os.WriteFile(path, mediafixture.WAV(1), 0600))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	cfg.ReviewMode = ReviewAlways
	cfg.InteractiveTTYAvailable = false
	cfg.MCPTimeout = time.Second
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	fs := toolregistry.FilesystemConfig{Root: root, SafeDirs: m.safeDirs()}
	require.NoError(t, toolregistry.RegisterImages(registry, fs))
	require.NoError(t, toolregistry.RegisterMedia(registry, fs))
	registry.ConfigureMedia(true, true, nil)
	args, _ := json.Marshal(map[string]any{"paths": []string{path}})
	result, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "read", Index: 1, Total: 1, Name: "fs_read_media", Arguments: args})
	require.NoError(t, err, result.Text)
	require.Len(t, result.Media, 1)
	require.Equal(t, "audio", result.Media[0].Kind)
	require.False(t, writeTargetToolAllowed(registry, "fs_read_media"))
	require.False(t, writeTargetToolAllowed(registry, "fs_find_media"))
}

func TestVideoToolCallerReadsExternalRecordingWithoutReview(t *testing.T) {
	root, external := t.TempDir(), t.TempDir()
	path := filepath.Join(external, "视频.mp4")
	require.NoError(t, os.WriteFile(path, mediafixture.Video(), 0600))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	cfg.ReviewMode = ReviewAlways
	cfg.InteractiveTTYAvailable = false
	cfg.MCPTimeout = time.Second
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg)}
	registry := toolregistry.NewRegistry()
	fs := toolregistry.FilesystemConfig{Root: root, SafeDirs: m.safeDirs()}
	require.NoError(t, toolregistry.RegisterImages(registry, fs))
	require.NoError(t, toolregistry.RegisterMedia(registry, fs))
	registry.ConfigureMedia(true, true, nil, true)
	args, _ := json.Marshal(map[string]any{"paths": []string{path}})
	result, err := m.toolResultCaller(registry, &cfg)(proto.ToolCallRequest{ID: "read", Index: 1, Total: 1, Name: "fs_read_media", Arguments: args})
	require.NoError(t, err, result.Text)
	require.Len(t, result.Media, 1)
	require.Equal(t, "video", result.Media[0].Kind)
	require.False(t, writeTargetToolAllowed(registry, "fs_read_media"))
	require.False(t, writeTargetToolAllowed(registry, "fs_find_media"))
}
