package app

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
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

	// read-only tools without directory semantics stay read-only.
	require.NoError(t, toolregistry.RegisterWebSearch(regFs, websearch.Config{Provider: "duckduckgo"}))
	intentWeb := buildAccessIntent("web_search", []byte(`{"query":"mods v2.5.0"}`), regFs, nil)
	require.Equal(t, AccessRead, intentWeb.Class)
	require.Empty(t, intentWeb.Dirs)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentWeb, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// registered tools without extractor and without read-only capability
	// still fail closed to writes.
	require.NoError(t, regFs.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "custom_tool"},
		Call: func(context.Context, json.RawMessage) (string, error) { return "", nil },
	}))
	intentCustom := buildAccessIntent("custom_tool", []byte(`{}`), regFs, nil)
	require.Equal(t, AccessWrite, intentCustom.Class)
	require.Empty(t, intentCustom.Dirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentCustom, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// unknown tool -> write fallback (fail-closed).
	intentUnk := buildAccessIntent("mcp_x", []byte(`{}`), regFs, nil)
	require.Equal(t, AccessWrite, intentUnk.Class)
	require.Empty(t, intentUnk.Dirs)

	// nil registry -> write fallback.
	intentNil := buildAccessIntent("anything", []byte(`{}`), nil, nil)
	require.Equal(t, AccessWrite, intentNil.Class)
}
