package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/testutil/mediafixture"
	"github.com/stretchr/testify/require"
)

func TestVideoDiscoveryReadLimitsAndGates(t *testing.T) {
	r, root := imageRegistry(t)
	require.NoError(t, RegisterMedia(r, FilesystemConfig{Root: root}))
	r.ConfigureMedia(true, true, nil, true)
	require.NoError(t, os.Mkdir(filepath.Join(root, ".hidden"), 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, ".hidden", "a.mp4"), mediafixture.Video(), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "b.mkv"), []byte("unsupported"), 0600))
	data, err := r.Call(context.Background(), "fs_find_media", []byte(`{"path":".","kinds":["video"],"recursive":true,"limit":1}`))
	require.NoError(t, err)
	var page imageScanPage
	require.NoError(t, json.Unmarshal([]byte(data), &page))
	require.NotEmpty(t, page.NextCursor)
	require.Equal(t, "video", page.Kinds[page.Paths[0]])
	args, _ := json.Marshal(map[string]any{"path": ".", "recursive": true, "kinds": []string{"audio"}, "cursor": page.NextCursor})
	_, err = r.Call(context.Background(), "fs_find_media", args)
	require.ErrorContains(t, err, "scope")
	result, err := r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":[".hidden/a.mp4",".hidden/a.mp4","b.mkv"]}`))
	require.NoError(t, err)
	require.Len(t, result.Media, 1)
	require.Equal(t, "video", result.Media[0].Kind)
	require.Contains(t, result.Text, "maximum 1 video")
	require.Contains(t, result.Text, "unread")
	_, err = r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":[".hidden/a.mp4"]}`))
	require.Error(t, err)
	r.ConfigureMedia(true, true, nil)
	_, err = r.Call(context.Background(), "fs_find_media", []byte(`{"path":".","kinds":["video"]}`))
	require.Error(t, err)
	_, err = r.CallResult(context.Background(), "fs_read_media", []byte(`{"paths":[".hidden/a.mp4"]}`))
	require.Error(t, err)
	a, err := scanKinds([]string{"video", "image", "audio", "video"})
	require.NoError(t, err)
	b, err := scanKinds([]string{"audio", "video", "image"})
	require.NoError(t, err)
	require.Equal(t, a, b)
}
