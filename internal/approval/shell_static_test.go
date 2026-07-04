package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAnalyzeShellStaticPOSIX(t *testing.T) {
	t.Run("read", func(t *testing.T) {
		got := AnalyzeShellStatic("git status && git diff", true)
		require.Equal(t, ShellStaticRead, got.Class)
		require.Empty(t, got.AffectedDirs)
		require.NotEmpty(t, got.Reason)
	})

	t.Run("write", func(t *testing.T) {
		got := AnalyzeShellStatic("cat > /tmp/out <<'EOF'\nhello\nEOF", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Contains(t, got.AffectedDirs, "/tmp")
		require.Contains(t, got.Reason, "static analysis")
	})

	t.Run("recursive remove targets directory", func(t *testing.T) {
		got := AnalyzeShellStatic("rm -rf ~/.ssh", true)
		require.Equal(t, ShellStaticWrite, got.Class)
		require.Equal(t, []string{"~/.ssh"}, got.AffectedDirs)
	})

	t.Run("unknown", func(t *testing.T) {
		got := AnalyzeShellStatic("some unsupported writer", true)
		require.Equal(t, ShellStaticUnknown, got.Class)
		require.Empty(t, got.AffectedDirs)
		require.Empty(t, got.Reason)
	})
}
