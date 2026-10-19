package tools

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPowerShellVersionCacheDeduplicatesConcurrentProbes(t *testing.T) {
	var cache sync.Map
	var calls atomic.Int32
	probe := func(context.Context, string) (string, error) { calls.Add(1); return "7.5.0", nil }
	path := filepath.Join(t.TempDir(), "pwsh.exe")
	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			version, err := cachedPowerShellVersion(context.Background(), &cache, path, probe)
			if err != nil || version != "7.5.0" {
				t.Errorf("version=%q err=%v", version, err)
			}
		})
	}
	wg.Wait()
	require.Equal(t, int32(1), calls.Load())
	_, err := cachedPowerShellVersion(context.Background(), &cache, filepath.Join(filepath.Dir(path), "powershell.exe"), probe)
	require.NoError(t, err)
	require.Equal(t, int32(2), calls.Load(), "different hosts must have independent probes")
}

func TestPowerShellVersionCachePreservesFailure(t *testing.T) {
	var cache sync.Map
	want := errors.New("probe failed")
	calls := 0
	for range 2 {
		version, err := cachedPowerShellVersion(context.Background(), &cache, "pwsh.exe", func(context.Context, string) (string, error) {
			calls++
			return "", want
		})
		require.Empty(t, version)
		require.ErrorIs(t, err, want)
	}
	require.Equal(t, 1, calls)
}

func TestPowerShellVersionCacheWindowsCaseAliases(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows path aliases")
	}
	var cache sync.Map
	calls := 0
	for _, path := range []string{`C:\PowerShell\pwsh.exe`, `c:\powershell\PWSH.EXE`} {
		_, err := cachedPowerShellVersion(context.Background(), &cache, path, func(context.Context, string) (string, error) { calls++; return "7.5.0", nil })
		require.NoError(t, err)
	}
	require.Equal(t, 1, calls)
}
