package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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

	analyzeDynamic := func(tool, cmd string) shellCommandAnalysis {
		return shellCommandAnalysis{
			NeedsReview:     true,
			Effect:          shellEffectWrite,
			UnresolvedPaths: []string{"$target"},
		}
	}
	intentDynamic := buildAccessIntent("shell_run", []byte(`{"command":"writer $target"}`), reg, analyzeDynamic)
	require.Equal(t, []string{"$target"}, intentDynamic.UnresolvedPaths)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentDynamic, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// Unknown effect remains write-like even when analyzer output is inconsistent.
	analyzeUnknown := func(tool, cmd string) shellCommandAnalysis {
		return shellCommandAnalysis{NeedsReview: false, Effect: shellEffectUnknown, AffectedDirs: []string{"/ws/opaque"}}
	}
	intentUnknown := buildAccessIntent("shell_run", []byte(`{"command":"opaque"}`), reg, analyzeUnknown)
	require.Equal(t, AccessWrite, intentUnknown.Class)
	require.Equal(t, []string{"/ws/opaque"}, intentUnknown.Dirs)

	// fs read via registered IntentExtractor.
	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: root}))
	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":"sub/a.txt"}`), regFs, nil)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Len(t, intentFs.Dirs, 1)

	// read-only tools without directory semantics stay read-only.
	require.NoError(t, toolregistry.RegisterWebSearch(regFs, websearch.Config{Provider: "tavily"}))
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

func TestBuildAccessIntentForProcessRun(t *testing.T) {
	root := t.TempDir()
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterProcess(reg, toolregistry.ProcessConfig{Root: root}))
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{Config: &cfg}
	canonicalRoot := cfg.ResolveWorkspace().Canonical

	read := buildAccessIntent("process_run", []byte(`{"program":"git","args":["status"]}`), reg, m.analyzeShellCommand)
	require.Equal(t, AccessRead, read.Class)
	require.Contains(t, read.Dirs, canonicalRoot)

	write := buildAccessIntent("process_run", []byte(`{"program":"rm","args":["out.txt"]}`), reg, m.analyzeShellCommand)
	require.Equal(t, AccessWrite, write.Class)
	require.Contains(t, write.Dirs, canonicalRoot)

	m.shellAnalyzer = func(tool, input string) shellCommandAnalysis {
		require.Equal(t, "process_run", tool)
		require.Contains(t, input, "direct_process_invocation")
		require.Contains(t, input, "no shell expansion")
		return shellCommandAnalysis{NeedsReview: false, Effect: shellEffectRead, Reason: "test classifier"}
	}
	unknown := buildAccessIntent("process_run", []byte(`{"program":"custom-tool","args":["$HOME",";rm"]}`), reg, m.analyzeShellCommand)
	require.Equal(t, AccessRead, unknown.Class)
	require.Contains(t, unknown.Dirs, canonicalRoot)
	for _, dir := range unknown.Dirs {
		require.NotEqual(t, filepath.Join(os.Getenv("HOME")), dir, "process argv must not expand $HOME")
	}

	literalHome := buildAccessIntent("process_run", []byte(`{"program":"rm","args":["$HOME/out.txt"]}`), reg, m.analyzeShellCommand)
	require.Equal(t, AccessWrite, literalHome.Class)
	require.Contains(t, literalHome.Dirs, canonicalRoot)
	for _, dir := range literalHome.Dirs {
		require.NotContains(t, dir, filepath.Join(os.Getenv("HOME"), "out.txt"))
	}
}

func TestMemoizedShellAnalyzerClassifiesEachCommandOnce(t *testing.T) {
	calls := 0
	analyze := memoizedShellAnalyzer(func(tool, command string) shellCommandAnalysis {
		calls++
		return shellCommandAnalysis{
			NeedsReview:  true,
			AffectedDirs: []string{"/tmp"},
			Reason:       tool + ": " + command,
			Effect:       shellEffectUnknown,
		}
	})

	first := analyze("shell_run", "opaque-command")
	second := analyze("shell_run", "opaque-command")
	require.Equal(t, first, second)
	require.Equal(t, 1, calls)

	analyze("shell_run", "different-command")
	require.Equal(t, 2, calls)
}
