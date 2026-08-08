package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestBuildAccessIntent(t *testing.T) {
	root := t.TempDir()
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(reg, toolregistry.ShellConfig{Root: root}))

	// shell read-only -> AccessRead + KnownDirs propagated.
	readAssessment := approval.CommandAssessment{Effect: approval.EffectRead, KnownDirs: []string{"/ws"}}
	intent := buildAccessIntent("shell_run", []byte(`{"command":"ls"}`), reg, &readAssessment)
	require.Equal(t, AccessRead, intent.Class)
	require.Equal(t, []string{"/ws"}, intent.Dirs)

	// shell mutable -> AccessWrite.
	writeAssessment := approval.CommandAssessment{Effect: approval.EffectWrite, KnownDirs: []string{"/ws/x"}}
	intentMut := buildAccessIntent("shell_run", []byte(`{"command":"rm x"}`), reg, &writeAssessment)
	require.Equal(t, AccessWrite, intentMut.Class)
	require.Equal(t, []string{"/ws/x"}, intentMut.Dirs)

	dynamicAssessment := approval.CommandAssessment{
		Effect:         approval.EffectWrite,
		DynamicTargets: []string{"$target"},
	}
	intentDynamic := buildAccessIntent("shell_run", []byte(`{"command":"writer $target"}`), reg, &dynamicAssessment)
	require.Equal(t, []string{"$target"}, intentDynamic.UnresolvedPaths)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentDynamic, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// Unknown effect remains write-like even when analyzer output is inconsistent.
	unknownAssessment := approval.CommandAssessment{Effect: approval.EffectUnknown, KnownDirs: []string{"/ws/opaque"}}
	intentUnknown := buildAccessIntent("shell_run", []byte(`{"command":"opaque"}`), reg, &unknownAssessment)
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

	readAssessment := m.assessCommand("process_run", `{"program":"git","args":["status"]}`)
	read := buildAccessIntent("process_run", nil, reg, &readAssessment)
	require.Equal(t, AccessRead, read.Class)
	require.Contains(t, read.Dirs, canonicalRoot)

	writeAssessment := m.assessCommand("process_run", `{"program":"rm","args":["out.txt"]}`)
	write := buildAccessIntent("process_run", nil, reg, &writeAssessment)
	require.Equal(t, AccessWrite, write.Class)
	require.Contains(t, write.Dirs, canonicalRoot)

	m.shellAnalyzer = func(tool, input string) approval.CommandAssessment {
		require.Equal(t, "process_run", tool)
		require.Contains(t, input, "direct_process_invocation")
		require.Contains(t, input, "no shell expansion")
		return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "test classifier"}
	}
	unknownAssessment := m.assessCommand("process_run", `{"program":"custom-tool","args":["$HOME",";rm"]}`)
	unknown := buildAccessIntent("process_run", nil, reg, &unknownAssessment)
	require.Equal(t, AccessRead, unknown.Class)
	require.Contains(t, unknown.Dirs, canonicalRoot)
	for _, dir := range unknown.Dirs {
		require.NotEqual(t, filepath.Join(os.Getenv("HOME")), dir, "process argv must not expand $HOME")
	}

	literalHomeAssessment := m.assessCommand("process_run", `{"program":"rm","args":["$HOME/out.txt"]}`)
	literalHome := buildAccessIntent("process_run", nil, reg, &literalHomeAssessment)
	require.Equal(t, AccessWrite, literalHome.Class)
	require.Contains(t, literalHome.Dirs, canonicalRoot)
	for _, dir := range literalHome.Dirs {
		require.NotContains(t, dir, filepath.Join(os.Getenv("HOME"), "out.txt"))
	}
}

func TestConstrainResolvedProcessAssessmentForWorkspaceExecutable(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{Config: &cfg}
	assessment := approval.CommandAssessment{
		Effect:    approval.EffectRead,
		KnownDirs: []string{root},
		Reason:    "allowlisted command",
	}

	got := m.constrainResolvedProcessAssessment(assessment, toolregistry.ProcessProgramBinding{
		Requested: "git",
		Resolved:  filepath.Join(root, "git"),
	})
	require.Equal(t, approval.EffectUnknown, got.Effect)
	require.Equal(t, assessment.KnownDirs, got.KnownDirs)
	require.Contains(t, got.Reason, "workspace or temporary directory")
}

func TestAssessProcessInvocationExplicitWorkspaceProgramStaysReviewable(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(tool, input string) approval.CommandAssessment {
			require.Equal(t, "process_run", tool)
			require.Contains(t, input, "direct_process_invocation")
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "test classifier"}
		},
	}

	tests := []struct {
		name   string
		invoke string
		expect approval.CommandEffect
	}{
		{
			name:   "relative workspace program stays reviewable",
			invoke: `{"program":"./tool.sh","args":["--run"]}`,
			expect: approval.EffectUnknown,
		},
		{
			name:   "absolute workspace program stays reviewable",
			invoke: fmt.Sprintf(`{"program":%q,"args":["--run"]}`, filepath.Join(root, "tool.sh")),
			expect: approval.EffectUnknown,
		},
		{
			name:   "external absolute program keeps classifier effect",
			invoke: `{"program":"/usr/bin/python3","args":["-c","pass"]}`,
			expect: approval.EffectRead,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := m.assessCommand("process_run", tc.invoke)
			require.Equal(t, tc.expect, assessment.Effect)
			if tc.expect == approval.EffectUnknown {
				require.Contains(t, assessment.Reason, "workspace or temporary directory")
			}
		})
	}
}
