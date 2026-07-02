package app

import (
	"testing"

	"github.com/stretchr/testify/require"
	toolregistry "github.com/panjie/mods/internal/tools"
)

func TestBuildAccessIntent(t *testing.T) {
	root := t.TempDir()
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(reg, toolregistry.ShellConfig{Root: root}))

	// shell read-only -> AccessRead + AffectedDirs propagated.
	analyze := func(tool, cmd string) shellCommandAnalysis {
		return shellCommandAnalysis{NeedsReview: false, AffectedDirs: []string{"/ws"}}
	}
	intent := buildAccessIntent("shell_run", []byte(`{"command":"ls"}`), reg, analyze)
	require.Equal(t, AccessRead, intent.Class)
	require.Equal(t, []string{"/ws"}, intent.Dirs)

	// shell mutable -> AccessWrite.
	analyzeMut := func(tool, cmd string) shellCommandAnalysis {
		return shellCommandAnalysis{NeedsReview: true, AffectedDirs: []string{"/ws/x"}}
	}
	intentMut := buildAccessIntent("shell_run", []byte(`{"command":"rm x"}`), reg, analyzeMut)
	require.Equal(t, AccessWrite, intentMut.Class)
	require.Equal(t, []string{"/ws/x"}, intentMut.Dirs)

	// fs read via registered IntentExtractor.
	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: root}))
	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":"sub/a.txt"}`), regFs, nil)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Len(t, intentFs.Dirs, 1)

	// unknown tool -> write fallback (fail-closed).
	intentUnk := buildAccessIntent("mcp_x", []byte(`{}`), regFs, nil)
	require.Equal(t, AccessWrite, intentUnk.Class)
	require.Empty(t, intentUnk.Dirs)

	// nil registry -> write fallback.
	intentNil := buildAccessIntent("anything", []byte(`{}`), nil, nil)
	require.Equal(t, AccessWrite, intentNil.Class)
}
