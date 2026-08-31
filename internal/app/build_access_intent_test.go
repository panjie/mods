package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
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

// TestSymlinkAliasWorkspacePathsClassifyAsWorkspace pins the fix for
// workspaces reached through a symlink (e.g. ~/.emacs.d -> real dir):
// fs and shell reads spelled through the alias must classify as workspace
// reads (auto-allow) while genuinely external paths still ask.
func TestSymlinkAliasWorkspacePathsClassifyAsWorkspace(t *testing.T) {
	realRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "lisp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "lisp", "init.el"), []byte("x"), 0o600))
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	scope := WorkspaceScope(canonicalTestPath(t, realRoot))
	aliasTarget := filepath.Join(aliasRoot, "lisp", "init.el")

	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: realRoot}))

	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":`+strconv.Quote(aliasTarget)+`}`), regFs, nil)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentFs, scope, nil, ApprovalReviewMode(ReviewAuto)))

	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = realRoot
	cfg.BuiltinTools.ShellReadOnlyCommands = []string{"cat"}
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(tool, command string) approval.CommandAssessment {
			t.Fatalf("LLM classifier should not be called for configured read-only command %q", command)
			return approval.UnknownCommandAssessment()
		},
	}
	t.Cleanup(func() { m.shellAnalyzer = nil })
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(reg, toolregistry.ShellConfig{Root: realRoot}))

	assessment := m.assessCommand("shell_run", "cat "+aliasTarget)
	intentShell := buildAccessIntent("shell_run", nil, reg, &assessment)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentShell, scope, nil, ApprovalReviewMode(ReviewAuto)))

	intentExt := buildAccessIntent("fs_read_file", []byte(`{"path":"/etc/passwd"}`), regFs, nil)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentExt, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

// TestAssessProcessInvocationSymlinkAliasWorkspaceProgramStaysReviewable
// checks that the security rule forcing review for executables living inside
// the workspace also fires when the program is spelled through a symlink
// alias of the workspace.
func TestAssessProcessInvocationSymlinkAliasWorkspaceProgramStaysReviewable(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "tool.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(root, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(tool, input string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectRead, Reason: "test classifier"}
		},
	}

	invoke := fmt.Sprintf(`{"program":%q,"args":["--run"]}`, filepath.Join(aliasRoot, "tool.sh"))
	assessment := m.assessCommand("process_run", invoke)
	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Contains(t, assessment.Reason, "workspace or temporary directory")
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
	require.Empty(t, unknown.Dirs)
	for _, dir := range unknown.Dirs {
		require.NotEqual(t, filepath.Join(os.Getenv("HOME")), dir, "process argv must not expand $HOME")
	}

	literalHomeAssessment := m.assessCommand("process_run", `{"program":"rm","args":["$HOME/out.txt"]}`)
	literalHome := buildAccessIntent("process_run", nil, reg, &literalHomeAssessment)
	require.Equal(t, AccessWrite, literalHome.Class)
	require.Contains(t, literalHome.Dirs, filepath.Join(canonicalRoot, "$HOME"))
	for _, dir := range literalHome.Dirs {
		require.NotContains(t, dir, filepath.Join(os.Getenv("HOME"), "out.txt"))
	}
}

func TestAssessProcessInvocationGitWorkspaceWritesAreStatic(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(_, input string) approval.CommandAssessment {
			t.Fatalf("effect classifier should not run for supported Git writes: %s", input)
			return approval.UnknownCommandAssessment()
		},
	}

	for _, invocation := range []string{
		`{"program":"git","args":["add","-A"]}`,
		`{"program":"git","args":["checkout","--","internal/app/render.go"]}`,
	} {
		assessment := m.assessCommand("process_run", invocation)
		require.Equal(t, approval.EffectWrite, assessment.Effect)
		require.Contains(t, assessment.KnownDirs, cfg.ResolveWorkspace().Canonical)
		require.Contains(t, assessment.KnownDirs, filepath.Join(cfg.ResolveWorkspace().Canonical, ".git"))
	}
}

func TestAssessProcessInvocationGitEnvironmentRedirectFailsClosed(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	classifierCalls := 0
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(_, _ string) approval.CommandAssessment {
			classifierCalls++
			return approval.UnknownCommandAssessment()
		},
	}

	assessment := m.assessCommand("process_run", `{"program":"git","args":["add","-A"],"secret_env":{"GIT_INDEX_FILE":"secret-ref"}}`)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Equal(t, 1, classifierCalls)
	require.Empty(t, assessment.KnownDirs)
}

func TestAssessProcessInvocationGitExternalRepositoryStateRequiresReview(t *testing.T) {
	for _, setup := range []struct {
		name string
		run  func(t *testing.T) (workspace, externalTarget string)
	}{
		{
			name: "workspace nested inside parent repository",
			run: func(t *testing.T) (string, string) {
				parent := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(parent, ".git"), 0o755))
				workspace := filepath.Join(parent, "nested-workspace")
				require.NoError(t, os.Mkdir(workspace, 0o755))
				return workspace, parent
			},
		},
		{
			name: "linked worktree metadata outside workspace",
			run: func(t *testing.T) (string, string) {
				workspace := t.TempDir()
				gitDir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(workspace, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
				return workspace, gitDir
			},
		},
	} {
		t.Run(setup.name, func(t *testing.T) {
			workspace, externalTarget := setup.run(t)
			cfg := defaultConfig()
			cfg.BuiltinTools.Workspace = workspace
			m := &Mods{
				Config: &cfg,
				shellAnalyzer: func(_, input string) approval.CommandAssessment {
					t.Fatalf("effect classifier should not run for deterministically scoped Git write: %s", input)
					return approval.UnknownCommandAssessment()
				},
			}

			assessment := m.assessCommand("process_run", `{"program":"git","args":["add","-A"]}`)
			intent := assessment.AccessIntent()

			require.Equal(t, approval.EffectWrite, assessment.Effect)
			require.Contains(t, assessment.KnownDirs, canonicalTestPath(t, externalTarget))
			require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkspaceScope(canonicalTestPath(t, workspace)), nil, ApprovalReviewMode(ReviewAuto)))
		})
	}
}

func TestAssessProcessGoInstallKeepsUnknownLocation(t *testing.T) {
	root := canonicalTestPath(t, t.TempDir())
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterProcess(reg, toolregistry.ProcessConfig{Root: root}))
	cfg := defaultConfig()
	cfg.BuiltinTools.Workspace = root
	m := &Mods{
		Config: &cfg,
		shellAnalyzer: func(tool, input string) approval.CommandAssessment {
			require.Equal(t, "process_run", tool)
			require.Contains(t, input, `"program":"go"`)
			return approval.CommandAssessment{
				Effect:    approval.EffectWrite,
				KnownDirs: []string{root, filepath.Join(root, "go.mod")},
				Reason:    "installer may update the workspace and module cache",
			}
		},
	}
	data := []byte(`{"program":"go","args":["install","github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"],"cwd":` + strconv.Quote(root) + `}`)

	assessment := m.assessCommand("process_run", string(data))
	require.Equal(t, approval.EffectWrite, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)

	intent := buildAccessIntent("process_run", data, reg, &assessment)
	require.Equal(t, AccessWrite, intent.Class)
	require.Empty(t, intent.Dirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkspaceScope(root), nil, ApprovalReviewMode(ReviewAuto), true))

	presentation := formatReviewPresentationWithIntent("process_run", data, assessment, WorkspaceScope(root), intent)
	require.Equal(t, "Modify an unknown target", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Target", Value: "Unknown"})
	require.Len(t, presentation.rows, 2)
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
