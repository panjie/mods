//go:build windows

package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/stretchr/testify/require"
)

func TestRunClosesPowerShellBridge(t *testing.T) {
	const helperEnv = "MODS_TEST_BRIDGE_LIFECYCLE"
	if os.Getenv(helperEnv) != "1" {
		// Run owns process-wide CLI and bridge state; isolate both in a child.
		temp := t.TempDir()
		t.Setenv("TMP", temp)
		t.Setenv("TEMP", temp)
		t.Setenv(helperEnv, "1")
		cmd := exec.Command(os.Args[0], "-test.run=^TestRunClosesPowerShellBridge$")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return
	}
	defer commandanalysis.CloseBridge()
	assessment := commandanalysis.AssessShellStaticWithContext("Get-Date", false, commandanalysis.ReadOnlyCommandPolicy{}, "")
	require.True(t, assessment.StaticRead, "%+v", assessment)
	scripts, err := filepath.Glob(filepath.Join(os.TempDir(), "mods-ps-bridge-*.ps1"))
	require.NoError(t, err)
	require.Len(t, scripts, 1, "the bridge must be started before exercising CLI shutdown")
	os.Args = []string{"mods", "--version"}
	require.Zero(t, Run("test", ""))
	require.NoFileExists(t, scripts[0], "Run must close the bridge and remove its script")
}
