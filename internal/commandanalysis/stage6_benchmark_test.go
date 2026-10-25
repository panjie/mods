package commandanalysis

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Cold includes host startup and shutdown. Warm reuses the bridge but parses a
// fresh request each iteration; no command result or mutable host fact is cached.
func BenchmarkPowerShellBridgeLifecycle(b *testing.B) {
	host := os.Getenv("MODS_TEST_PWSH")
	if host == "" {
		b.Skip("set MODS_TEST_PWSH for a real host")
	}
	script := filepath.Join(b.TempDir(), "bridge.ps1")
	require.NoError(b, os.WriteFile(script, psBridgeScript, 0600))
	start := func() *bridgeProcess {
		cmd := exec.Command(host, "-NoProfile", "-NonInteractive", "-File", script)
		stdin, err := cmd.StdinPipe()
		require.NoError(b, err)
		stdout, err := cmd.StdoutPipe()
		require.NoError(b, err)
		require.NoError(b, cmd.Start())
		return &bridgeProcess{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout)}
	}
	source := "Get-Content 'input.txt'"
	b.Run("cold", func(b *testing.B) {
		for b.Loop() {
			bp := start()
			_, err := bp.roundTripContext(context.Background(), source)
			bp.shutdown()
			require.NoError(b, err)
		}
	})
	b.Run("warm", func(b *testing.B) {
		bp := start()
		b.Cleanup(bp.shutdown)
		_, err := bp.roundTripContext(context.Background(), source)
		require.NoError(b, err)
		b.ResetTimer()
		for b.Loop() {
			_, err := bp.roundTripContext(context.Background(), source)
			require.NoError(b, err)
		}
	})
}
