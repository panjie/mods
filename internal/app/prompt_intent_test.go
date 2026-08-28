package app

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestParsePromptIntentClassifierResponse(t *testing.T) {
	intents := parsePromptIntentClassifierResponse(`{"intents":["workspace-edit","global-read"]}`)
	require.Equal(t, []approval.PromptIntent{approval.IntentWorkspaceEdit, approval.IntentGlobalRead}, intents)

	intents = parsePromptIntentClassifierResponse("```json\n{\"intents\":[\"global-read\"]}\n```")
	require.Equal(t, []approval.PromptIntent{approval.IntentGlobalRead}, intents)

	intents = parsePromptIntentClassifierResponse(`prefix {"intents":["workspace-edit"]} suffix`)
	require.Equal(t, []approval.PromptIntent{approval.IntentWorkspaceEdit}, intents)

	require.Empty(t, parsePromptIntentClassifierResponse(`{"intents":["git-commit"]}`))
	require.Empty(t, parsePromptIntentClassifierResponse(``))
	require.Empty(t, parsePromptIntentClassifierResponse(`not json at all`))
}

func TestParseWriteScopeClassifierResponse(t *testing.T) {
	require.Equal(t, []approval.WriteScope{approval.WriteScopeWorkspace},
		parseWriteScopeClassifierResponse(`{"scopes":["workspace"]}`))

	// A valid empty array means "no local filesystem write".
	require.Equal(t, []approval.WriteScope{}, parseWriteScopeClassifierResponse(`{"scopes":[]}`))

	require.Equal(t, []approval.WriteScope{approval.WriteScopeWorkspace, approval.WriteScopeExternal},
		parseWriteScopeClassifierResponse("```json\n{\"scopes\":[\"workspace\",\"external\"]}\n```"))

	// Any unrecognized label fails the whole response (fail-closed).
	require.Nil(t, parseWriteScopeClassifierResponse(`{"scopes":["bogus"]}`))
	require.Nil(t, parseWriteScopeClassifierResponse(`{"scopes":["workspace","bogus"]}`))
	require.Nil(t, parseWriteScopeClassifierResponse(``))
	require.Nil(t, parseWriteScopeClassifierResponse(`not json`))
}

func promptIntentDeps(intent AccessIntent, assessment approval.CommandAssessment, intents []approval.PromptIntent, writeScopes []approval.WriteScope, shell bool) reviewerDeps {
	return reviewerDeps{
		ctx:            context.Background(),
		shellExecution: shell,
		assessment:     &assessment,
		accessIntent:   intent,
		promptIntents:  intents,
		writeScopes:    writeScopes,
	}
}

func TestPromptIntentGateGlobalRead(t *testing.T) {
	workspaceScope := testShellWorkspaceScope(t)
	globalRead := []approval.PromptIntent{approval.IntentGlobalRead}
	externalDir := "/external"
	if runtime.GOOS == "windows" {
		externalDir = `C:\external`
	}

	t.Run("external read is granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessRead, Dirs: []string{externalDir}}
		var traces []approvalTrace
		deps := promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectRead}, globalRead, nil, false)
		deps.onDecision = func(trace approvalTrace) { traces = append(traces, trace) }
		err := reviewer.requestApproval(deps, "fs_read_file", []byte(`{"path":"/external/x"}`))
		require.NoError(t, err)
		require.Len(t, traces, 1)
		require.Equal(t, "prompt intent", traces[0].Source)
		require.Equal(t, "global-read", traces[0].Detail)
	})

	t.Run("dynamic read is granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		assessment := approval.CommandAssessment{Effect: approval.EffectRead, DynamicTargets: []string{"$PROFILE"}}
		intent := assessment.AccessIntent()
		err := reviewer.requestApproval(promptIntentDeps(intent, assessment, globalRead, nil, true), "powershell_run", []byte(`{"command":"Get-Content $PROFILE"}`))
		require.NoError(t, err)
	})

	t.Run("global-read does not cover writes", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{workspaceScope.Value}}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, globalRead, nil, false), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})
}

func TestPromptIntentGateWorkspaceEdit(t *testing.T) {
	workspaceScope := testShellWorkspaceScope(t)
	workspaceEdit := []approval.PromptIntent{approval.IntentWorkspaceEdit}
	externalDir := "/external"
	if runtime.GOOS == "windows" {
		externalDir = `C:\external`
	}

	t.Run("workspace write is granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{workspaceScope.Value}}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, nil, false), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.NoError(t, err)
	})

	t.Run("empty-dir workspace write is granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		deps := promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, []approval.WriteScope{approval.WriteScopeWorkspace}, true)
		var traces []approvalTrace
		deps.onDecision = func(trace approvalTrace) { traces = append(traces, trace) }
		err := reviewer.requestApproval(deps, "shell_run", []byte(`{"command":"git add -A"}`))
		require.NoError(t, err)
		require.Equal(t, "workspace-edit", traces[0].Detail)
	})

	t.Run("empty-dir write without classifier result is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, nil, true), "shell_run", []byte(`{"command":"git add -A"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("empty-dir external write is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, []approval.WriteScope{approval.WriteScopeExternal}, true), "shell_run", []byte(`{"command":"write-etc"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("empty-dir unknown write is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, []approval.WriteScope{approval.WriteScopeUnknown}, true), "shell_run", []byte(`{"command":"mystery-binary"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("empty-dir workspace write without workspace-edit is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, []approval.PromptIntent{approval.IntentGlobalRead}, []approval.WriteScope{approval.WriteScopeWorkspace}, true), "shell_run", []byte(`{"command":"git add -A"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("external write is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{externalDir}}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, nil, false), "fs_write_file", []byte(`{"path":"/external/x","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("dynamic write is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		assessment := approval.CommandAssessment{Effect: approval.EffectWrite, DynamicTargets: []string{"$target"}}
		intent := assessment.AccessIntent()
		err := reviewer.requestApproval(promptIntentDeps(intent, assessment, workspaceEdit, nil, true), "powershell_run", []byte(`{"command":"Remove-Item $target"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("workspace-edit does not cover reads", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessRead, Dirs: []string{externalDir}}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectRead}, workspaceEdit, nil, false), "fs_read_file", []byte(`{"path":"/external/x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("review always bypasses the gate", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAlways, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite, Dirs: []string{workspaceScope.Value}}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, nil, false), "fs_write_file", []byte(`{"path":"out.txt","content":"x"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})

	t.Run("unknown effect is not granted", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		assessment := approval.CommandAssessment{Effect: approval.EffectUnknown}
		intent := assessment.AccessIntent()
		err := reviewer.requestApproval(promptIntentDeps(intent, assessment, workspaceEdit, nil, true), "shell_run", []byte(`{"command":"opaque-command"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})
}

func TestPromptIntentGateRemoteWrite(t *testing.T) {
	workspaceScope := testShellWorkspaceScope(t)
	workspaceEdit := []approval.PromptIntent{approval.IntentWorkspaceEdit}

	t.Run("purely remote write is granted without any intent", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		deps := promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, []approval.WriteScope{}, true)
		var traces []approvalTrace
		deps.onDecision = func(trace approvalTrace) { traces = append(traces, trace) }
		err := reviewer.requestApproval(deps, "shell_run", []byte(`{"command":"git push"}`))
		require.NoError(t, err)
		require.Equal(t, "remote", traces[0].Detail)
	})

	t.Run("workspace + remote requires workspace-edit", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{Class: AccessWrite}
		deps := promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, workspaceEdit, []approval.WriteScope{approval.WriteScopeWorkspace}, true)
		err := reviewer.requestApproval(deps, "shell_run", []byte(`{"command":"git push"}`))
		require.NoError(t, err)
	})
}

func TestPromptIntentGateMixedIntent(t *testing.T) {
	workspaceScope := testShellWorkspaceScope(t)
	externalDir := "/external"
	if runtime.GOOS == "windows" {
		externalDir = `C:\external`
	}
	both := []approval.PromptIntent{approval.IntentWorkspaceEdit, approval.IntentGlobalRead}

	t.Run("copy external read + workspace write is granted by both intents", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{
			ReadDirs:  []string{externalDir},
			WriteDirs: []string{workspaceScope.Value},
		}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, both, nil, false), "fs_copy", []byte(`{"source_path":"/external/x","dest_path":"out.txt"}`))
		require.NoError(t, err)
	})

	t.Run("copy external read is not granted by workspace-edit alone", func(t *testing.T) {
		reviewer := &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope}
		intent := AccessIntent{
			ReadDirs:  []string{externalDir},
			WriteDirs: []string{workspaceScope.Value},
		}
		err := reviewer.requestApproval(promptIntentDeps(intent, approval.CommandAssessment{Effect: approval.EffectWrite}, []approval.PromptIntent{approval.IntentWorkspaceEdit}, nil, false), "fs_copy", []byte(`{"source_path":"/external/x","dest_path":"out.txt"}`))
		require.ErrorIs(t, err, errReviewUnavailable)
	})
}

func TestToolCallerPromptIntentWiring(t *testing.T) {
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"},
		Capabilities: toolregistry.ToolCapabilities{
			Mutable:        true,
			ShellExecution: true,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			return "staged", nil
		},
	}))
	workspaceScope := testShellWorkspaceScope(t)

	newMods := func(scopes []approval.WriteScope, intents []approval.PromptIntent) *Mods {
		cfg := defaultConfig()
		cfg.PromptIntent = true
		cfg.Minimal = true // disable the command-preflight correction
		cfg.BuiltinTools.Workspace = workspaceScope.Value
		m := &Mods{
			ctx:                 context.Background(),
			Config:              &cfg,
			currentToolRegistry: registry,
			reviewer:            &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope},
			shellAnalyzer: func(string, string) approval.CommandAssessment {
				return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "git add stages the index"}
			},
			promptIntentAnalyzer: func(string) []approval.PromptIntent {
				return intents
			},
			writeScopeClassifier: func(string, string) []approval.WriteScope {
				return scopes
			},
		}
		return m
	}

	call := func(m *Mods) error {
		_, err := m.toolCaller(registry, m.Config, "commit and push the local changes")(proto.ToolCallRequest{
			ID: "call-1", Index: 1, Total: 1, Name: "shell_run",
			Arguments: []byte(`{"command":"git add -A"}`),
		})
		return err
	}

	workspaceEdit := []approval.PromptIntent{approval.IntentWorkspaceEdit}

	t.Run("workspace-scoped empty-dir write skips review", func(t *testing.T) {
		require.NoError(t, call(newMods([]approval.WriteScope{approval.WriteScopeWorkspace}, workspaceEdit)))
	})

	t.Run("remote-scoped write skips review", func(t *testing.T) {
		require.NoError(t, call(newMods([]approval.WriteScope{}, workspaceEdit)))
	})

	t.Run("unknown-scoped write still asks", func(t *testing.T) {
		require.ErrorIs(t, call(newMods([]approval.WriteScope{approval.WriteScopeUnknown}, workspaceEdit)), errReviewUnavailable)
	})

	t.Run("external-scoped write still asks", func(t *testing.T) {
		require.ErrorIs(t, call(newMods([]approval.WriteScope{approval.WriteScopeExternal}, workspaceEdit)), errReviewUnavailable)
	})

	t.Run("write without any intent still asks", func(t *testing.T) {
		require.ErrorIs(t, call(newMods([]approval.WriteScope{approval.WriteScopeWorkspace}, nil)), errReviewUnavailable)
	})
}

func TestToolCallerPromptIntentUsesPrefix(t *testing.T) {
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{Name: "shell_run"},
		Capabilities: toolregistry.ToolCapabilities{
			Mutable:        true,
			ShellExecution: true,
		},
		Call: func(context.Context, json.RawMessage) (string, error) {
			return "staged", nil
		},
	}))
	workspaceScope := testShellWorkspaceScope(t)

	cfg := defaultConfig()
	cfg.PromptIntent = true
	cfg.Minimal = true
	cfg.Prefix = "commit and push the local changes"
	cfg.BuiltinTools.Workspace = workspaceScope.Value

	var classified string
	m := &Mods{
		ctx:                 context.Background(),
		Config:              &cfg,
		currentToolRegistry: registry,
		reviewer:            &toolReviewer{reviewMode: ReviewAuto, scope: workspaceScope},
		shellAnalyzer: func(string, string) approval.CommandAssessment {
			return approval.CommandAssessment{Effect: approval.EffectWrite, Reason: "git add stages the index"}
		},
		promptIntentAnalyzer: func(prompt string) []approval.PromptIntent {
			classified = prompt
			return []approval.PromptIntent{approval.IntentWorkspaceEdit}
		},
		writeScopeClassifier: func(string, string) []approval.WriteScope {
			return []approval.WriteScope{approval.WriteScopeWorkspace}
		},
	}

	// Empty content: the prompt arrives via cfg.Prefix (CLI args form).
	_, err := m.toolCaller(registry, m.Config, "")(proto.ToolCallRequest{
		ID: "call-1", Index: 1, Total: 1, Name: "shell_run",
		Arguments: []byte(`{"command":"git add -A"}`),
	})
	require.NoError(t, err)
	require.Equal(t, "commit and push the local changes", classified)
}
