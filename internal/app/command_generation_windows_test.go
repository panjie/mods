package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/panjie/mods/internal/evals"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

// Exercise the documented cmdlet semantics against real synthetic bytes. This
// is a fixed read-only command, never execution of a model's generated script.
func TestGenerationPowerShellAggregationSemantics(t *testing.T) {
	root := t.TempDir()
	for name, content := range evals.CommandGenerationFiles() {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
		require.NoError(t, os.WriteFile(path, []byte(content), 0600))
	}
	for _, tc := range []struct{ name, command, want string }{
		{"files", "Get-ChildItem -Recurse -File -Filter *.go | Measure-Object | Select-Object -ExpandProperty Count", "3"},
		{"physical", "Get-ChildItem -Recurse -File -Filter *.go | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object | Select-Object -ExpandProperty Count", "6"},
		{"nonempty", "Get-ChildItem -Recurse -File -Filter *.go | ForEach-Object { Get-Content -LiteralPath $_.FullName } | Measure-Object -Line | Select-Object -ExpandProperty Lines", "4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := exec.Command(toolregistry.SelectedShellInfo().Executable, "-NoProfile", "-NonInteractive", "-Command", tc.command)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			require.NoError(t, err, string(out))
			require.Equal(t, tc.want, strings.TrimSpace(string(out)))
		})
	}
}
