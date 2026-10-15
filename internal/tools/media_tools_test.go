package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

func TestMixedMediaScanReadAndGate(t *testing.T) {
	r, root := imageRegistry(t)
	require.NoError(t, RegisterMedia(r, FilesystemConfig{Root: root}))
	r.ConfigureMedia(true, true, nil)
	writeTestImage(t, filepath.Join(root, "a.png"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.wav"), mediafixture.WAV(1), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "c.m4a"), []byte("unsupported"), 0600))
	page, err := r.Call(context.Background(), "fs_find_media", []byte(`{"path":".","recursive":true,"kinds":["image","audio"]}`))
	require.NoError(t, err)
	require.Contains(t, page, "c.m4a")
	require.Contains(t, page, `"b.wav":"audio"`)
	result, err := r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":["b.wav","a.png","c.m4a"]}`))
	require.NoError(t, err)
	require.Len(t, result.Media, 2)
	require.Equal(t, "audio", result.Media[0].Kind)
	require.Equal(t, "image", result.Media[1].Kind)
	require.Contains(t, result.Text, "unread")
	require.Contains(t, r.ImageSearchWarning(), "1 discovered file(s)")
	r.ConfigureMedia(true, false, nil)
	_, err = r.Call(context.Background(), "fs_find_media", []byte(`{"path":".","kinds":["audio"]}`))
	require.Error(t, err)
	result, err = r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":["b.wav"]}`))
	require.Error(t, err)
	require.Empty(t, result.Media)
	_, err = r.Call(context.Background(), "fs_find_images", []byte(`{"path":".","kinds":["audio"]}`))
	require.Error(t, err)
}
func TestMediaCursorScopeAndSharedBudget(t *testing.T) {
	r, root := imageRegistry(t)
	require.NoError(t, RegisterMedia(r, FilesystemConfig{Root: root}))
	r.ConfigureMedia(true, true, nil)
	for _, name := range []string{"a.wav", "b.wav"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), mediafixture.WAV(1), 0600))
	}
	data, err := r.Call(context.Background(), "fs_find_media", []byte(`{"path":".","kinds":["audio"],"limit":1}`))
	require.NoError(t, err)
	var page imageScanPage
	require.NoError(t, json.Unmarshal([]byte(data), &page))
	require.NotEmpty(t, page.NextCursor)
	args, _ := json.Marshal(map[string]any{"path": ".", "kinds": []string{"image"}, "cursor": page.NextCursor})
	_, err = r.Call(context.Background(), "fs_find_media", args)
	require.ErrorContains(t, err, "scope")
	r.ConfigureMedia(true, true, []proto.Message{{Media: []proto.MediaAttachment{{Kind: "image", Data: make([]byte, 20*1024*1024-1)}}}})
	result, err := r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":["a.wav"]}`))
	require.Error(t, err)
	require.Contains(t, result.Text, "budget exhausted")
	require.Empty(t, result.Media)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.CallResult(ctx, "fs_read_media", []byte(`{"paths":["a.wav"]}`))
	require.Error(t, err)
}

func TestAudioBatchLimitAndImageAlias(t *testing.T) {
	r, root := imageRegistry(t)
	require.NoError(t, RegisterMedia(r, FilesystemConfig{Root: root}))
	r.ConfigureMedia(true, true, nil)
	require.NoError(t, os.WriteFile(filepath.Join(root, "a.wav"), mediafixture.WAV(1), 0600))
	result, err := r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":["a.wav","a.wav","a.wav","a.wav","a.wav"]}`))
	require.NoError(t, err)
	require.Len(t, result.Media, 4)
	require.Contains(t, result.Text, "maximum 4 recordings")
	result, err = r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":["a.wav"]}`))
	require.Error(t, err)
	require.Empty(t, result.Media)
	r.ConfigureMedia(false, false, nil)
	require.NotContains(t, r.tools, "fs_read_media")
	require.NotContains(t, r.tools, "fs_read_images")
}
