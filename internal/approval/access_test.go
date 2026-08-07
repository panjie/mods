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
		{"read in workspace", AccessIntent{Class: AccessRead, Dirs: []string{ws.Value}}, DecisionAllow},
		{"write in workspace", AccessIntent{Class: AccessWrite, Dirs: []string{ws.Value}}, DecisionAsk},
		{"read in temp", AccessIntent{Class: AccessRead, Dirs: []string{tempDir}}, DecisionAllow},
		{"write in temp", AccessIntent{Class: AccessWrite, Dirs: []string{tempDir}}, DecisionAllow},
		{"read external", AccessIntent{Class: AccessRead, Dirs: []string{external}}, DecisionAsk},
		{"write external", AccessIntent{Class: AccessWrite, Dirs: []string{external}}, DecisionAsk},
		{"read empty dirs", AccessIntent{Class: AccessRead}, DecisionAllow},
		{"write empty dirs", AccessIntent{Class: AccessWrite}, DecisionAsk},
		{"mixed ws+external read", AccessIntent{Class: AccessRead, Dirs: []string{ws.Value, external}}, DecisionAsk},
		{"write spanning temp and external", AccessIntent{Class: AccessWrite, Dirs: []string{tempDir, external}}, DecisionAsk},
		{"copy external source to temp", AccessIntent{ReadDirs: []string{external}, WriteDirs: []string{tempDir}}, DecisionAsk},
		{"copy workspace source to temp", AccessIntent{ReadDirs: []string{ws.Value}, WriteDirs: []string{tempDir}}, DecisionAllow},
		{"copy workspace source to workspace", AccessIntent{ReadDirs: []string{ws.Value}, WriteDirs: []string{ws.Value}}, DecisionAsk},
		{"missing access intent fails closed", AccessIntent{}, DecisionAsk},
		{"dynamic probe without concrete dirs", AccessIntent{Class: AccessRead, UnresolvedPaths: []string{"$target"}, DynamicProbe: true}, DecisionAllow},
		{"dynamic content read without concrete dirs", AccessIntent{Class: AccessRead, UnresolvedPaths: []string{"$target"}}, DecisionAsk},
		{"dynamic read with concrete dir", AccessIntent{Class: AccessRead, Dirs: []string{ws.Value}, UnresolvedPaths: []string{"$target"}}, DecisionAsk},
		{"dynamic write target fails closed", AccessIntent{Class: AccessWrite, Dirs: []string{ws.Value}, UnresolvedPaths: []string{"$target"}}, DecisionAsk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ClassifyAccess(c.intent, ws, safeDirs, ReviewAuto))
		})
	}
}

func TestRulesCannotAuthorizeUnresolvedPaths(t *testing.T) {
	ws := WorkspaceScope(t.TempDir())
	rules := RulesForDirs([]string{ws.Value}, ws, AccessWrite)
	intent := AccessIntent{Class: AccessWrite, Dirs: []string{ws.Value}, UnresolvedPaths: []string{"$PROFILE.CurrentUserCurrentHost"}}
	require.False(t, RulesAllowIntent(rules, intent, ws, nil, ReviewAuto))
}

func TestClassifyAccessModeOverride(t *testing.T) {
	ws := wsScope(t)
	external := filepath.Clean(t.TempDir())
	read := AccessIntent{Class: AccessRead, Dirs: []string{external}}
	require.Equal(t, DecisionAllow, ClassifyAccess(read, ws, nil, ReviewNever))
	require.Equal(t, DecisionAllow, ClassifyAccess(AccessIntent{Class: AccessWrite, UnresolvedPaths: []string{"$target"}}, ws, nil, ReviewNever))
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
