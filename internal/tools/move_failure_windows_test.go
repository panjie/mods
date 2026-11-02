//go:build windows

package tools

import (
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"testing"
)

func TestMoveFailurePreservesDestination(t *testing.T) {
	dir := t.TempDir()
	source, dest := filepath.Join(dir, "source"), filepath.Join(dir, "dest")
	require.NoError(t, os.WriteFile(source, []byte("source"), 0600))
	require.NoError(t, os.WriteFile(dest, []byte("original"), 0600))
	p, err := windows.UTF16PtrFromString(source)
	require.NoError(t, err)
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	require.NoError(t, err)
	defer windows.CloseHandle(h)
	_, err = movePath(source, dest, true)
	require.Error(t, err)
	got, err := os.ReadFile(dest)
	require.NoError(t, err)
	require.Equal(t, "original", string(got))
}
