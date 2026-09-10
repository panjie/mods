package tools

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCopyPathRejectsSameFileTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	_, err := copyPath(source, source, false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got), "source content must survive a self-copy")
}

func TestCopyPathRejectsCopyIntoSameDirectory(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	_, err := copyPath(source, dir, false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
}

func TestCopyPathRejectsHardLinkDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	dest := filepath.Join(dir, "b.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))
	require.NoError(t, os.Link(source, dest))

	_, err := copyPath(source, dest, false, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
}

func TestMovePathRejectsSameFileTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	_, err := movePath(source, source, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got), "source must not be deleted by a self-move")
}

func TestMovePathRejectsSameFileWithoutOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	_, err := movePath(source, source, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
}

func TestMovePathRejectsHardLinkDestination(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	dest := filepath.Join(dir, "b.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))
	require.NoError(t, os.Link(source, dest))

	_, err := movePath(source, dest, true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "same file")

	got, readErr := os.ReadFile(source)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
}

func TestCopyPathStillCopiesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	dest := filepath.Join(dir, "b.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	finalDest, err := copyPath(source, dest, false, false)
	require.NoError(t, err)
	require.Equal(t, dest, finalDest)

	got, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
	_, statErr := os.Stat(source)
	require.NoError(t, statErr)
}

func TestMovePathStillMovesDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "a.txt")
	dest := filepath.Join(dir, "b.txt")
	require.NoError(t, os.WriteFile(source, []byte("precious"), 0o644))

	finalDest, err := movePath(source, dest, false)
	require.NoError(t, err)
	require.Equal(t, dest, finalDest)

	got, readErr := os.ReadFile(dest)
	require.NoError(t, readErr)
	require.Equal(t, "precious", string(got))
	_, statErr := os.Stat(source)
	require.ErrorIs(t, statErr, os.ErrNotExist)
}
