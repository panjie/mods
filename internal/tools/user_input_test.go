package tools

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRegisterUserInput(t *testing.T) {
	registry := NewRegistry()
	var got UserInputRequest
	require.NoError(t, RegisterUserInput(registry, func(_ context.Context, req UserInputRequest) (UserInputResponse, error) {
		got = req
		return UserInputResponse{Answer: "prod"}, nil
	}))
	require.True(t, registry.Interactive(UserInputToolName))
	require.Equal(t, TimeoutPolicySelf, registry.TimeoutPolicy(UserInputToolName))
	out, err := registry.Call(context.Background(), UserInputToolName, json.RawMessage(`{"question":"Environment?","kind":"select","options":["dev","prod"]}`))
	require.NoError(t, err)
	require.Equal(t, "Environment?", got.Question)
	require.JSONEq(t, `{"answer":"prod"}`, out)
}

func TestUserInputValidation(t *testing.T) {
	tests := []UserInputRequest{
		{Question: "", Kind: "text"},
		{Question: "Pick", Kind: "select", Options: []string{"one"}},
		{Question: "Secret", Kind: "secret"},
		{Question: "Bad", Kind: "unknown"},
	}
	for _, req := range tests {
		require.Error(t, validateUserInputRequest(req))
	}
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "Password", Kind: "secret",
		Target: UserInputTarget{Tool: "db_query", Path: "/password"},
	}))
}

func TestShellSecretEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell syntax")
	}
	registry := NewRegistry()
	require.NoError(t, RegisterShell(registry, ShellConfig{Root: t.TempDir()}))
	out, err := registry.Call(context.Background(), "shell_run", json.RawMessage(`{"command":"printf %s \"$DB_PASSWORD\"","secret_env":{"DB_PASSWORD":"resolved-secret"}}`))
	require.NoError(t, err)
	require.Equal(t, "resolved-secret", out)
}
