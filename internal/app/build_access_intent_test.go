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
	"github.com/panjie/mods/internal/commandanalysis"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/websearch"
	"github.com/stretchr/testify/require"
)

func TestBuildAccessIntent(t *testing.T) {
	root := t.TempDir()
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(reg, toolregistry.ShellConfig{Root: root}))

	// Legacy directory display alone never creates target authority.
	readAssessment := approval.CommandAssessment{Evidence: approval.EvidenceStatic, WriteCoverage: approval.CoverageComplete, Effect: approval.EffectRead, KnownDirs: []string{"/ws"}}
	intent := buildAccessIntent("shell_run", []byte(`{"command":"ls"}`), reg, &readAssessment)
	require.Equal(t, AccessRead, intent.Class)
	require.Empty(t, intent.AllDirs())

	// shell mutable -> AccessWrite.
	writeAssessment := approval.CommandAssessment{Evidence: approval.EvidenceStatic, WriteCoverage: approval.CoverageComplete, Effect: approval.EffectWrite, KnownDirs: []string{"/ws/x"}}
	intentMut := buildAccessIntent("shell_run", []byte(`{"command":"rm x"}`), reg, &writeAssessment)
	require.Equal(t, AccessWrite, intentMut.Class)
	require.Empty(t, intentMut.WriteDirs)
	require.False(t, intentMut.HasCompleteWriteTargets())

	dynamicAssessment := approval.CommandAssessment{Evidence: approval.EvidenceStatic, WriteCoverage: approval.CoverageComplete,
		Effect:         approval.EffectWrite,
		Targets:        []commandanalysis.Target{{Kind: commandanalysis.TargetFilesystem, Role: commandanalysis.TargetWrite, Operation: commandanalysis.OperationFollowLeaf, Raw: "$target"}},
		DynamicTargets: []string{"$target"},
	}
	intentDynamic := buildAccessIntent("shell_run", []byte(`{"command":"writer $target"}`), reg, &dynamicAssessment)
	require.Equal(t, []string{"$target"}, intentDynamic.UnresolvedPaths)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentDynamic, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// Unknown effect remains write-like even when analyzer output is inconsistent.
	unknownAssessment := approval.CommandAssessment{Evidence: approval.EvidenceStatic, WriteCoverage: approval.CoverageComplete, Effect: approval.EffectUnknown, KnownDirs: []string{"/ws/opaque"}}
	intentUnknown := buildAccessIntent("shell_run", []byte(`{"command":"opaque"}`), reg, &unknownAssessment)
	require.Equal(t, AccessWrite, intentUnknown.Class)
	require.Empty(t, intentUnknown.WriteDirs)
	require.False(t, intentUnknown.HasCompleteWriteTargets())

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
	require.Equal(t, DecisionAllow, ClassifyAccess(intentWeb, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// registered tools without extractor and without read-only capability
	// still fail closed to writes.
	require.NoError(t, regFs.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "custom_tool"},
		Call: func(context.Context, json.RawMessage) (string, error) { return "", nil },
	}))
	intentCustom := buildAccessIntent("custom_tool", []byte(`{}`), regFs, nil)
	require.Equal(t, AccessWrite, intentCustom.Class)
	require.Empty(t, intentCustom.Dirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(intentCustom, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	// unknown tool -> write fallback (fail-closed).
	intentUnk := buildAccessIntent("mcp_x", []byte(`{}`), regFs, nil)
	require.Equal(t, AccessWrite, intentUnk.Class)
	require.Empty(t, intentUnk.Dirs)

	// nil registry -> write fallback.
	intentNil := buildAccessIntent("anything", []byte(`{}`), nil, nil)
	require.Equal(t, AccessWrite, intentNil.Class)
}

// TestSymlinkAliasWorkingDirPathsClassifyAsWorkingDir pins the fix for
// cwds reached through a symlink (e.g. ~/.emacs.d -> real dir):
// fs and shell reads spelled through the alias must classify as cwd
// reads; genuinely external reads are also allowed by the write-only policy.
func TestSymlinkAliasWorkingDirPathsClassifyAsWorkingDir(t *testing.T) {
	realRoot := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "lisp"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(realRoot, "lisp", "init.el"), []byte("x"), 0o600))
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	scope := WorkingDirScope(canonicalTestPath(t, realRoot))
	aliasTarget := filepath.Join(aliasRoot, "lisp", "init.el")

	regFs := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterFilesystem(regFs, toolregistry.FilesystemConfig{Root: realRoot}))

	intentFs := buildAccessIntent("fs_read_file", []byte(`{"path":`+strconv.Quote(aliasTarget)+`}`), regFs, nil)
	require.Equal(t, AccessRead, intentFs.Class)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentFs, scope, nil, ApprovalReviewMode(ReviewAuto)))

	cfg := defaultConfig()
	cfg.WorkingDir = realRoot
	cfg.BuiltinTools.ShellReadOnlyCommands = []string{"cat"}
	m := &Mods{
		Config: &cfg,
	}

	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterShell(reg, toolregistry.ShellConfig{Root: realRoot}))

	assessment := m.assessCommand("shell_run", "cat "+aliasTarget)
	intentShell := buildAccessIntent("shell_run", nil, reg, &assessment)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentShell, scope, nil, ApprovalReviewMode(ReviewAuto)))

	intentExt := buildAccessIntent("fs_read_file", []byte(`{"path":"/etc/passwd"}`), regFs, nil)
	require.Equal(t, DecisionAllow, ClassifyAccess(intentExt, scope, nil, ApprovalReviewMode(ReviewAuto)))
}

// TestAssessProcessInvocationSymlinkAliasWorkingDirProgramStaysReviewable
// checks that the security rule forcing review for executables living inside
// the cwd also fires when the program is spelled through a symlink
// alias of the cwd.
func TestAssessProcessInvocationSymlinkAliasWorkingDirProgramStaysReviewable(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "tool.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755))
	aliasRoot := filepath.Join(t.TempDir(), "alias-root")
	if err := os.Symlink(root, aliasRoot); err != nil {
		t.Skipf("symlink creation not supported (requires admin on Windows): %v", err)
	}
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{
		Config: &cfg,
	}

	invoke := fmt.Sprintf(`{"program":%q,"args":["--run"]}`, filepath.Join(aliasRoot, "tool.sh"))
	assessment := m.assessCommand("process_run", invoke)
	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Contains(t, assessment.Reason, "cwd or temporary directory")
}

func TestBuildAccessIntentForProcessRun(t *testing.T) {
	root := t.TempDir()
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterProcess(reg, toolregistry.ProcessConfig{Root: root}))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{Config: &cfg}
	canonicalRoot := cfg.ResolveWorkingDir().Canonical

	readAssessment := m.assessCommand("process_run", `{"program":"git","args":["status"]}`)
	read := buildAccessIntent("process_run", nil, reg, &readAssessment)
	require.Equal(t, AccessRead, read.Class)
	require.Empty(t, read.WriteDirs)

	writeAssessment := m.assessCommand("process_run", `{"program":"rm","args":["out.txt"]}`)
	write := buildAccessIntent("process_run", nil, reg, &writeAssessment)
	require.Equal(t, AccessWrite, write.Class)
	require.Contains(t, write.WriteDirs, canonicalRoot)

	unknownAssessment := m.assessCommand("process_run", `{"program":"custom-tool","args":["$HOME",";rm"]}`)
	unknown := buildAccessIntent("process_run", nil, reg, &unknownAssessment)
	require.Equal(t, AccessWrite, unknown.Class)
	require.Empty(t, unknown.Dirs)
	for _, dir := range unknown.Dirs {
		require.NotEqual(t, filepath.Join(os.Getenv("HOME")), dir, "process argv must not expand $HOME")
	}

	literalHomeAssessment := m.assessCommand("process_run", `{"program":"rm","args":["$HOME/out.txt"]}`)
	literalHome := buildAccessIntent("process_run", nil, reg, &literalHomeAssessment)
	require.Equal(t, AccessWrite, literalHome.Class)
	require.Contains(t, literalHome.WriteDirs, filepath.Join(canonicalRoot, "$HOME"))
	for _, dir := range literalHome.WriteDirs {
		require.NotContains(t, dir, filepath.Join(os.Getenv("HOME"), "out.txt"))
	}
}

func TestAssessProcessInvocationGitWorkingDirWritesAreStatic(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{
		Config: &cfg,
	}

	for _, invocation := range []string{
		`{"program":"git","args":["add","-A"]}`,
		`{"program":"git","args":["checkout","--","internal/app/render.go"]}`,
	} {
		assessment := m.assessCommand("process_run", invocation)
		require.Equal(t, approval.EffectWrite, assessment.Effect)
		require.Contains(t, assessment.KnownDirs, cfg.ResolveWorkingDir().Canonical)
		require.Contains(t, assessment.KnownDirs, filepath.Join(cfg.ResolveWorkingDir().Canonical, ".git"))
	}
}

func TestAssessProcessInvocationGitEnvironmentRedirectFailsClosed(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, ".git"), 0o755))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{
		Config: &cfg,
	}

	assessment := m.assessCommand("process_run", `{"program":"git","args":["add","-A"],"secret_env":{"GIT_INDEX_FILE":"secret-ref"}}`)

	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)
}

func TestAssessProcessInvocationGitExternalRepositoryStateRequiresReview(t *testing.T) {
	for _, setup := range []struct {
		name string
		run  func(t *testing.T) (cwd, externalTarget string)
	}{
		{
			name: "cwd nested inside parent repository",
			run: func(t *testing.T) (string, string) {
				parent := t.TempDir()
				require.NoError(t, os.Mkdir(filepath.Join(parent, ".git"), 0o755))
				cwd := filepath.Join(parent, "nested-cwd")
				require.NoError(t, os.Mkdir(cwd, 0o755))
				return cwd, parent
			},
		},
		{
			name: "linked worktree metadata outside cwd",
			run: func(t *testing.T) (string, string) {
				cwd := t.TempDir()
				gitDir := t.TempDir()
				require.NoError(t, os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o600))
				return cwd, gitDir
			},
		},
	} {
		t.Run(setup.name, func(t *testing.T) {
			cwd, externalTarget := setup.run(t)
			cfg := defaultConfig()
			cfg.WorkingDir = cwd
			m := &Mods{
				Config: &cfg,
			}

			assessment := m.assessCommand("process_run", `{"program":"git","args":["add","-A"]}`)
			intent := assessment.AccessIntent()

			require.Equal(t, approval.EffectWrite, assessment.Effect)
			require.Contains(t, assessment.KnownDirs, canonicalTestPath(t, externalTarget))
			require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(canonicalTestPath(t, cwd)), nil, ApprovalReviewMode(ReviewAuto)))
		})
	}
}

func TestAssessProcessGoInstallKeepsUnknownLocation(t *testing.T) {
	root := canonicalTestPath(t, t.TempDir())
	reg := toolregistry.NewRegistry()
	require.NoError(t, toolregistry.RegisterProcess(reg, toolregistry.ProcessConfig{Root: root}))
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{
		Config: &cfg,
	}
	data := []byte(`{"program":"go","args":["install","github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest"],"cwd":` + strconv.Quote(root) + `}`)

	assessment := m.assessCommand("process_run", string(data))
	require.Equal(t, approval.EffectUnknown, assessment.Effect)
	require.Empty(t, assessment.KnownDirs)

	intent := buildAccessIntent("process_run", data, reg, &assessment)
	require.Equal(t, AccessWrite, intent.Class)
	require.Empty(t, intent.Dirs)
	require.Equal(t, DecisionAsk, ClassifyAccess(intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))
	require.Empty(t, candidateRulesForIntent(intent, WorkingDirScope(root), nil, ApprovalReviewMode(ReviewAuto)))

	presentation := formatReviewPresentationWithIntent("process_run", data, assessment, WorkingDirScope(root), intent)
	require.Equal(t, "Run with unknown effects", presentation.headline)
	require.Contains(t, presentation.rows, interactionRow{Label: "Target", Value: "Unknown"})
	require.Len(t, presentation.rows, 3)
}

func TestConstrainResolvedProcessAssessmentForWorkingDirExecutable(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{Config: &cfg}
	assessment := approval.CommandAssessment{Evidence: approval.EvidenceStatic, WriteCoverage: approval.CoverageComplete,
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
	require.Contains(t, got.Reason, "cwd or temporary directory")
}

func TestAssessProcessInvocationExplicitWorkingDirProgramStaysReviewable(t *testing.T) {
	root := t.TempDir()
	cfg := defaultConfig()
	cfg.WorkingDir = root
	m := &Mods{
		Config: &cfg,
	}

	tests := []struct {
		name       string
		invoke     string
		expect     approval.CommandEffect
		wantReason string
	}{
		{
			name:       "relative cwd program stays reviewable",
			invoke:     `{"program":"./tool.sh","args":["--run"]}`,
			expect:     approval.EffectUnknown,
			wantReason: "cwd or temporary directory",
		},
		{
			name:       "absolute cwd program stays reviewable",
			invoke:     fmt.Sprintf(`{"program":%q,"args":["--run"]}`, filepath.Join(root, "tool.sh")),
			expect:     approval.EffectUnknown,
			wantReason: "cwd or temporary directory",
		},
		{
			name:   "external program requires review",
			invoke: `{"program":"/usr/local/bin/deploy","args":["--dry-run"]}`,
			expect: approval.EffectUnknown,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assessment := m.assessCommand("process_run", tc.invoke)
			require.Equal(t, tc.expect, assessment.Effect)
			if tc.wantReason != "" {
				require.Contains(t, assessment.Reason, tc.wantReason)
			}
		})
	}
}

// TestAssessProcessInvocationExternalProgramPathIsNotATarget pins that a
// program's own path is execution context, never an affected directory: an
// external executable such as "C:\Program Files\Neovim\bin\nvim.exe" must not
// turn its install directory into a review target or a "write dirs" approval
// candidate. Literal argument paths remain path facts.
func TestAssessProcessInvocationExternalProgramPathIsNotATarget(t *testing.T) {
	cwd := t.TempDir()
	scope := WorkingDirScope(cwd)
	external := func(parts ...string) string {
		if volume := filepath.VolumeName(cwd); volume != "" {
			return filepath.Join(append([]string{volume + `\`}, parts...)...)
		}
		return "/" + filepath.Join(parts...)
	}

	t.Run("unknown effect fails closed without a program-dir write rule", func(t *testing.T) {
		m := &Mods{
			Config: testConfigForWorkingDir(cwd),
		}
		invoke := fmt.Sprintf(`{"program":%q,"args":["--headless","+luafile init.lua"]}`, external("external-pgm", "tool.exe"))
		assessment := m.assessCommand("process_run", invoke)
		require.Equal(t, approval.EffectUnknown, assessment.Effect)
		require.Empty(t, assessment.KnownDirs)
		intent := normalizeAccessIntentDirs(assessment.AccessIntent(), cwd, "process_run", true)
		require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
		require.Empty(t, candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
	})

	t.Run("unknown process keeps argument facts without the program dir", func(t *testing.T) {
		m := &Mods{
			Config: testConfigForWorkingDir(cwd),
		}
		target := external("external-out", "out.txt")
		invoke := fmt.Sprintf(`{"program":%q,"args":["--out",%q]}`, external("external-pgm", "tool.exe"), target)
		assessment := m.assessCommand("process_run", invoke)
		require.Equal(t, approval.EffectUnknown, assessment.Effect)
		require.Equal(t, []string{target}, assessment.KnownDirs)
		intent := normalizeAccessIntentDirs(assessment.AccessIntent(), cwd, "process_run", true)
		require.Equal(t, []string{target}, intent.Dirs)
		require.Equal(t, DecisionAsk, ClassifyAccess(intent, scope, nil, ApprovalReviewMode(ReviewAuto)))
		rules := candidateRulesForIntent(intent, scope, nil, ApprovalReviewMode(ReviewAuto))
		require.Empty(t, rules)
	})
}
