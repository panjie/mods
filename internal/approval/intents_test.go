package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParsePromptIntent(t *testing.T) {
	for label, expected := range map[string]PromptIntent{
		"workspace-edit":   IntentWorkspaceEdit,
		" Workspace-Edit ": IntentWorkspaceEdit,
		"global-read":      IntentGlobalRead,
		"Global-Read":      IntentGlobalRead,
	} {
		intent, ok := ParsePromptIntent(label)
		require.True(t, ok, label)
		require.Equal(t, expected, intent)
	}
	for _, label := range []string{"", "unknown", "git-commit", "git-push", "read", "edit", "workspace-edit,global-read"} {
		_, ok := ParsePromptIntent(label)
		require.False(t, ok, label)
	}
}

func TestParsePromptIntentResponse(t *testing.T) {
	intents := ParsePromptIntentResponse(`{"intents":["workspace-edit","global-read"]}`)
	require.Equal(t, []PromptIntent{IntentWorkspaceEdit, IntentGlobalRead}, intents)

	intents = ParsePromptIntentResponse(`{"intents":["workspace-edit","workspace-edit","git-commit","bogus"]}`)
	require.Equal(t, []PromptIntent{IntentWorkspaceEdit}, intents)

	require.Empty(t, ParsePromptIntentResponse(`{"intents":[]}`))
	require.Empty(t, ParsePromptIntentResponse(`{"intens":["workspace-edit"]}`))
	require.Empty(t, ParsePromptIntentResponse(`not json`))
}

func TestParseWriteScope(t *testing.T) {
	for label, expected := range map[string]WriteScope{
		"workspace":   WriteScopeWorkspace,
		" Workspace ": WriteScopeWorkspace,
		"external":    WriteScopeExternal,
		"unknown":     WriteScopeUnknown,
		"UNKNOWN":     WriteScopeUnknown,
	} {
		scope, ok := ParseWriteScope(label)
		require.True(t, ok, label)
		require.Equal(t, expected, scope)
	}
	for _, label := range []string{"", "remote", "workspace,external", "bogus"} {
		_, ok := ParseWriteScope(label)
		require.False(t, ok, label)
	}
}

func TestParseWriteScopeResponse(t *testing.T) {
	require.Equal(t, []WriteScope{WriteScopeWorkspace}, ParseWriteScopeResponse(`{"scopes":["workspace"]}`))
	require.Equal(t, []WriteScope{}, ParseWriteScopeResponse(`{"scopes":[]}`))
	require.Equal(t, []WriteScope{WriteScopeWorkspace, WriteScopeExternal},
		ParseWriteScopeResponse(`{"scopes":["workspace","external"]}`))

	require.Nil(t, ParseWriteScopeResponse(`{"scopes":["bogus"]}`))
	require.Nil(t, ParseWriteScopeResponse(`{"scopes":["workspace","bogus"]}`))
	require.Nil(t, ParseWriteScopeResponse(`{"scopes":["workspace","workspace"]}`))
	require.Nil(t, ParseWriteScopeResponse(`not json`))
	require.Nil(t, ParseWriteScopeResponse(`{"intens":["workspace"]}`))
}
