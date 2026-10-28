package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestBuildAccessIntent(t *testing.T) {
	root := t.TempDir()
	// fs read via registered IntentExtractor.
	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: root}))
	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":"sub/a.txt"}`), regFs)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Len(t, intentFs.Dirs, 1)

	// read-only tools without directory semantics stay read-only.
	require.NoError(t, toolregistry.RegisterWebSearch(regFs, websearch.Config{Provider: "tavily"}))
	intentWeb := buildAccessIntent("web_search", []byte(`{"query":"mods v2.5.0"}`), regFs)
	require.Equal(t, AccessRead, intentWeb.Class)
	require.Empty(t, intentWeb.Dirs)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentWeb, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// registered tools without extractor and without read-only capability
	// still fail closed to writes.
	require.NoError(t, regFs.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "custom_tool"},
		Call: func(context.Context, json.RawMessage) (string, error) { return "", nil },
	}))
	intentCustom := buildAccessIntent("custom_tool", []byte(`{}`), regFs)
	require.Equal(t, AccessWrite, intentCustom.Class)
	require.Empty(t, intentCustom.Dirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentCustom, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// unknown tool -> write fallback (fail-closed).
	intentUnk := buildAccessIntent("mcp_x", []byte(`{}`), regFs)
	require.Equal(t, AccessWrite, intentUnk.Class)
	require.Empty(t, intentUnk.Dirs)

	// nil registry -> write fallback.
	intentNil := buildAccessIntent("anything", []byte(`{}`), nil)
	require.Equal(t, AccessWrite, intentNil.Class)
}

// TestSymlinkAliasWorkingDirPathsClassifyAsWorkingDir pins the fix for
// cwds reached through a symlink (e.g. ~/.emacs.d -> real dir):
// Native file reads spelled through the alias must classify as cwd
// reads; genuinely external reads are also allowed by the write-only policy.
func TestSymlinkAliasWorkingDirPathsClassifyAsWorkingDir(t *testing.T) {
	realRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "lisp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "lisp", "init.el"), []byte("x"), 0o600))
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	canonical, err := filepath.EvalSymlinks(realRoot)
	require.NoError(t, err)
	scope := WorkingDirScope(canonical)
	aliasTarget := filepath.Join(aliasRoot, "lisp", "init.el")

	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: realRoot}))

	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":`+strconv.Quote(aliasTarget)+`}`), regFs)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentFs, scope, nil, ApprovalReviewMode(ReviewAuto)))

	intentExt := buildAccessIntent("fs_read_file", []byte(`{"path":"/etc/passwd"}`), regFs)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentExt, scope, nil, ApprovalReviewMode(ReviewAuto)))
}
