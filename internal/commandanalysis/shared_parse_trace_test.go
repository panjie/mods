//go:build commandtrace

package commandanalysis

import (
	"context"
	"testing"

	"github.com/panjie/mods/internal/commandtrace"
	"github.com/stretchr/testify/require"
)

func TestAnalyzeParsesPOSIXOnce(t *testing.T) {
	for _, source := range []string{codeCountCommand, anchoredPackageQueryLoop, packageQueryLoop, "cat /external/file", "trusted-reader /external/file", "cat <<'EOF'\n/path/in/heredoc\nEOF", "touch out; unknown > /tmp/file", "cat $(unknown)", "git status; git push origin", "cat '", ""} {
		t.Run(source, func(t *testing.T) {
			e := NewEngine(Settings{ReadOnlyCommands: []string{"trusted-reader"}}, Dependencies{Git: gitFunc(func(context.Context, GitQuery) (string, error) { return "https://example.com/repo", nil })})
			for i := 0; i < 2; i++ {
				before := commandtrace.Snapshot()
				_, err := e.Analyze(context.Background(), shellInput(source))
				require.NoError(t, err)
				counts := commandtrace.Snapshot().Since(before)
				require.EqualValues(t, 1, counts.POSIXParses)
				require.Zero(t, counts.BridgeParses)
			}
		})
	}
}
