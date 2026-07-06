package approval

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func wsScope(t *testing.T) Scope {
	t.Helper()
	return WorkspaceScope(filepath.Clean(t.TempDir()))
}

func TestClassifyAccessMatrix(t *testing.T) {
	ws := wsScope(t)
	tempDir := filepath.Clean(t.TempDir())
	safeDirs := []string{tempDir}
	external := filepath.Clean(t.TempDir())

	cases := []struct {
		name   string
		intent AccessIntent
		want   Decision
	}{
		{"read in workspace", AccessIntent{AccessRead, []string{ws.Value}, ""}, DecisionAllow},
		{"write in workspace", AccessIntent{AccessWrite, []string{ws.Value}, ""}, DecisionAsk},
		{"read in temp", AccessIntent{AccessRead, []string{tempDir}, ""}, DecisionAllow},
		{"write in temp", AccessIntent{AccessWrite, []string{tempDir}, ""}, DecisionAllow},
		{"read external", AccessIntent{AccessRead, []string{external}, ""}, DecisionAsk},
		{"write external", AccessIntent{AccessWrite, []string{external}, ""}, DecisionAsk},
		{"read empty dirs", AccessIntent{AccessRead, nil, ""}, DecisionAllow},
		{"write empty dirs", AccessIntent{AccessWrite, nil, ""}, DecisionAsk},
		{"mixed ws+external read", AccessIntent{AccessRead, []string{ws.Value, external}, ""}, DecisionAsk},
		{"write spanning temp and external", AccessIntent{AccessWrite, []string{tempDir, external}, ""}, DecisionAsk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ClassifyAccess(c.intent, ws, safeDirs, ReviewAuto))
		})
	}
}

func TestClassifyAccessModeOverride(t *testing.T) {
	ws := wsScope(t)
	external := filepath.Clean(t.TempDir())
	read := AccessIntent{AccessRead, []string{external}, ""}
	require.Equal(t, DecisionAllow, ClassifyAccess(read, ws, nil, ReviewNever))
}

func TestLocateDir(t *testing.T) {
	ws := wsScope(t)
	tempDir := filepath.Clean(t.TempDir())
	safeDirs := []string{tempDir}
	external := filepath.Clean(t.TempDir())

	require.Equal(t, locWorkspace, locateDir(filepath.Join(ws.Value, "a.txt"), ws, safeDirs))
	require.Equal(t, locWorkspace, locateDir("relative/path", ws, safeDirs), "relative path treated as workspace-local")
	require.Equal(t, locTemp, locateDir(filepath.Join(tempDir, "x"), ws, safeDirs))
	require.Equal(t, locExternal, locateDir(external, ws, safeDirs))
	require.Equal(t, locUnknown, locateDir("", ws, safeDirs))
}
