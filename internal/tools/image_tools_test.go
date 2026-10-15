package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	imageutil "github.com/panjie/mods/internal/image"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

func imageRegistry(t *testing.T) (*Registry, string) {
	t.Helper()
	root := t.TempDir()
	r := NewRegistry()
	require.NoError(t, RegisterImages(r, FilesystemConfig{Root: root}))
	return r, root
}
func writeTestImage(t *testing.T, path string) {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2))))
	require.NoError(t, os.WriteFile(path, b.Bytes(), 0600))
}
func scanPage(t *testing.T, r *Registry, args map[string]any) imageScanPage {
	t.Helper()
	b, err := json.Marshal(args)
	require.NoError(t, err)
	out, err := r.Call(context.Background(), "fs_find_images", b)
	require.NoError(t, err)
	var page imageScanPage
	require.NoError(t, json.Unmarshal([]byte(out), &page))
	return page
}
func TestImageScanPagination(t *testing.T) {
	r, root := imageRegistry(t)
	require.NoError(t, os.Mkdir(filepath.Join(root, ".隐藏"), 0700))
	for i := 0; i < 1005; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(root, ".隐藏", fmt.Sprintf("%04d.PNG", i)), nil, 0600))
	}
	writeTestImage(t, filepath.Join(root, "a 空格.jpg"))
	shallow := scanPage(t, r, map[string]any{"path": "."})
	require.Equal(t, []string{"a 空格.jpg"}, shallow.Paths)
	require.True(t, shallow.Complete)
	seen := map[string]bool{}
	cursor := ""
	for i := 0; i < 30; i++ {
		page := scanPage(t, r, map[string]any{"path": ".", "recursive": true, "cursor": cursor, "limit": 100})
		for _, p := range page.Paths {
			require.False(t, seen[p], "duplicate %s", p)
			seen[p] = true
		}
		if page.Complete {
			require.Empty(t, page.NextCursor)
			break
		}
		require.NotEmpty(t, page.NextCursor)
		cursor = page.NextCursor
	}
	require.Len(t, seen, 1006)
	require.Contains(t, r.ImageSearchWarning(), "1006 discovered")
	_, err := r.Call(context.Background(), "fs_find_images", []byte(fmt.Sprintf(`{"path":".","recursive":true,"cursor":%q}`, cursor)))
	require.ErrorContains(t, err, "expired")
}
func TestReadImagesPreservesSourcesAndPartialFailure(t *testing.T) {
	r, root := imageRegistry(t)
	writeTestImage(t, filepath.Join(root, "a.jpg"))
	require.NoError(t, os.WriteFile(filepath.Join(root, "bad.png"), []byte("not pixels"), 0600))
	page := scanPage(t, r, map[string]any{"path": "."})
	require.Len(t, page.Paths, 2)
	result, err := r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":["bad.png","a.jpg"]}`))
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
	require.Equal(t, "a.jpg", result.Images[0].Source)
	require.Equal(t, "image/png", result.Images[0].MimeType)
	require.Contains(t, result.Text, "bad.png\": unread")
	require.Contains(t, r.ImageSearchWarning(), "1 discovered")
	_, err = r.Call(context.Background(), "fs_read_images", []byte(`{"paths":["a.jpg"]}`))
	require.ErrorContains(t, err, "multimodal caller")
}
func TestImageBudgetIncludesHistory(t *testing.T) {
	r, root := imageRegistry(t)
	writeTestImage(t, filepath.Join(root, "a.png"))
	r.ConfigureImages(true, []proto.Message{{Images: []proto.Image{{Data: make([]byte, imageutil.MaxTotalImageBytes)}}}})
	result, err := r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":["a.png"]}`))
	require.Error(t, err)
	require.Empty(t, result.Images)
	require.Contains(t, result.Text, "budget exhausted")
}
func TestImageScanReadCompletionAndGate(t *testing.T) {
	r, root := imageRegistry(t)
	writeTestImage(t, filepath.Join(root, "a.png"))
	writeTestImage(t, filepath.Join(root, "b.png"))
	first := scanPage(t, r, map[string]any{"path": ".", "limit": 1})
	require.False(t, first.Complete)
	require.Contains(t, r.ImageSearchWarning(), "1 scan(s) not finished")
	second := scanPage(t, r, map[string]any{"path": ".", "limit": 1, "cursor": first.NextCursor})
	require.True(t, second.Complete)
	_, err := r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":["a.png","b.png"]}`))
	require.NoError(t, err)
	require.Empty(t, r.ImageSearchWarning())
	r.ConfigureImages(false, nil)
	_, ok := r.Tool("fs_read_images")
	require.False(t, ok)
	_, ok = r.Tool("fs_find_images")
	require.False(t, ok)
}
func TestImageScanCancellationAndScope(t *testing.T) {
	r, root := imageRegistry(t)
	writeTestImage(t, filepath.Join(root, "a.png"))
	writeTestImage(t, filepath.Join(root, "b.png"))
	page := scanPage(t, r, map[string]any{"path": ".", "limit": 1})
	args := []byte(fmt.Sprintf(`{"path":".","recursive":true,"cursor":%q}`, page.NextCursor))
	_, err := r.Call(context.Background(), "fs_find_images", args)
	require.ErrorContains(t, err, "scope")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = r.Call(ctx, "fs_find_images", []byte(`{"path":"."}`))
	require.ErrorIs(t, err, context.Canceled)
	_, err = r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":[""]}`))
	require.Error(t, err)
}
func TestImageScanSymlinkAndOutsideRead(t *testing.T) {
	r, root := imageRegistry(t)
	outside := t.TempDir()
	writeTestImage(t, filepath.Join(outside, "外.png"))
	if err := os.Symlink(root, filepath.Join(root, "loop")); err != nil {
		t.Skip(err)
	}
	page := scanPage(t, r, map[string]any{"path": ".", "recursive": true})
	require.True(t, page.Complete)
	require.Len(t, page.Issues, 1)
	data, _ := json.Marshal(map[string]any{"paths": []string{filepath.Join(outside, "外.png")}})
	_, err := r.CallResult(context.Background(), "fs_read_images", data)
	require.Error(t, err)
	result, err := r.CallResult(WithAuthorizedDirs(context.Background(), []string{outside}), "fs_read_images", data)
	require.NoError(t, err)
	require.Len(t, result.Images, 1)
}

func TestImageScanDirectoryChangeRequiresRestart(t *testing.T) {
	r, root := imageRegistry(t)
	writeTestImage(t, filepath.Join(root, "a.png"))
	writeTestImage(t, filepath.Join(root, "b.png"))
	page := scanPage(t, r, map[string]any{"path": ".", "limit": 1})
	writeTestImage(t, filepath.Join(root, "c.png"))
	// Force a distinct directory timestamp even on filesystems with coarse clocks.
	stamp := time.Now().Add(2 * time.Second)
	require.NoError(t, os.Chtimes(root, stamp, stamp))
	_, err := r.Call(context.Background(), "fs_find_images", []byte(fmt.Sprintf(`{"path":".","cursor":%q}`, page.NextCursor)))
	require.ErrorContains(t, err, "changed during image scan")
	restarted := scanPage(t, r, map[string]any{"path": "."})
	require.True(t, restarted.Complete)
	require.Len(t, restarted.Paths, 3)
	_, err = r.CallResult(context.Background(), "fs_read_images", []byte(`{"paths":["a.png","b.png","c.png"]}`))
	require.NoError(t, err)
	require.Empty(t, r.ImageSearchWarning())
}
