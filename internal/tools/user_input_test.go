package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
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

func TestRegisterUserInputDescriptionGuidesSecrets(t *testing.T) {
	registry := NewRegistry()
	require.NoError(t, RegisterUserInput(registry, nil))
	specs := registry.Specs()
	require.Len(t, specs, 1)

	desc := specs[0].Description
	for _, want := range []string{
		"Call this tool, not assistant text",
		"one short sentence",
		"1-3 words",
		"placeholder",
		"Never enumerate numbered options",
		"kind=form",
		"secret_ref",
		"secret_env",
		"/secret_env/OA_PASSWORD",
		"powershell_run",
		"$env:OA_PASSWORD",
	} {
		require.Contains(t, desc, want)
	}
	require.Less(t, strings.Count(desc, "Complete form example"), 2)
}

func TestRegisterUserInputMultiselect(t *testing.T) {
	registry := NewRegistry()
	var got UserInputRequest
	require.NoError(t, RegisterUserInput(registry, func(_ context.Context, req UserInputRequest) (UserInputResponse, error) {
		got = req
		return UserInputResponse{Answers: []string{"tests", "docs"}}, nil
	}))
	out, err := registry.Call(context.Background(), UserInputToolName, json.RawMessage(
		`{"question":"What should run?","kind":"multiselect","options":["tests","lint","docs"]}`,
	))
	require.NoError(t, err)
	require.Equal(t, "multiselect", got.Kind)
	require.Equal(t, []string{"tests", "lint", "docs"}, got.Options)
	require.JSONEq(t, `{"answers":["tests","docs"]}`, out)
}

func TestUserInputValidation(t *testing.T) {
	tests := []UserInputRequest{
		{Question: "", Kind: "text"},
		{Question: "Pick", Kind: "select", Options: []string{"one"}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one"}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one", "one"}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one", ""}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one", "two"}, Multiline: true},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one", "two"}, Target: UserInputTarget{Tool: "x", Path: "/y"}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"one", "two"}, Fields: []UserInputField{{Key: "a", Label: "A", Kind: "text"}}},
		{Question: "Secret", Kind: "secret"},
		{Question: "Bad", Kind: "unknown"},
		{Question: "Form", Kind: "form"},
		{Question: "Form", Kind: "form", Fields: []UserInputField{}},
		{Question: "Form", Kind: "form", Options: []string{"x"}, Fields: []UserInputField{{Key: "a", Label: "A", Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "bad key", Label: "A", Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "1up", Label: "A", Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "", Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{
			{Key: "a", Label: "A", Kind: "text"},
			{Key: "a", Label: "A2", Kind: "text"},
		}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "secret"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "select"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "select", Options: []string{"only"}}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "multiselect", Options: []string{"one", "two"}}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "text", Target: UserInputTarget{Tool: "x", Path: "/y"}}}},
		{Question: "Text", Kind: "text", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "text"}}},
		{Question: "multi\nline", Kind: "text"},
		{Question: strings.Repeat("x", maxUserInputQuestionRunes+1), Kind: "text"},
		{Question: "Pick", Kind: "select", Options: []string{"one", strings.Repeat("x", maxUserInputOptionRunes+1)}},
		{Question: "Pick", Kind: "select", Options: []string{"one\ntwo", "three"}},
		{Question: "Pick", Kind: "select", Options: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		{Question: "Pick", Kind: "multiselect", Options: []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: strings.Repeat("x", maxUserInputLabelRunes+1), Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "two\nlines", Kind: "text"}}},
		{Question: "Form", Kind: "form", Fields: []UserInputField{{Key: "a", Label: "A", Kind: "text", Placeholder: strings.Repeat("x", maxUserInputPlaceholderRunes+1)}}},
		{Question: "哪个环节失败？1 是交叉编译 mods.exe？2 是实机运行失败？3 其他？", Kind: "text"},
		{Question: "Pick: 1. cross-compile 2. run on Windows 3. other", Kind: "text"},
		{Question: "选一个 ①WSL ②虚拟机 ③容器", Kind: "text"},
		{Question: "1、虚拟机 2、WSL，选哪个？", Kind: "text"},
	}
	for _, req := range tests {
		require.Error(t, validateUserInputRequest(req), "expected error for %+v", req)
	}
	// Ordinary numbers are not enumerations.
	require.NoError(t, validateUserInputRequest(UserInputRequest{Question: "Use go 1.22 or 1.23?", Kind: "text"}))
	require.NoError(t, validateUserInputRequest(UserInputRequest{Question: "Which port, 8080 or 3000?", Kind: "text"}))
	// The same enumeration is fine once moved into select options.
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "哪个环节失败？", Kind: "select",
		Options: []string{"交叉编译 mods.exe", "实机运行失败", "其他"},
	}))
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "Password", Kind: "secret",
		Target: UserInputTarget{Tool: "db_query", Path: "/password"},
	}))
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "Pick", Kind: "multiselect", Options: []string{"one", "two"},
	}))
	// Exactly at the option cap is valid.
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "Pick", Kind: "multiselect", Options: []string{
			"a", "b", "c", "d", "e", "f", "g", "h",
		},
	}))
}

func TestUserInputFormValidation(t *testing.T) {
	// Mixed form: text + secret + select, with per-field targets and options.
	require.NoError(t, validateUserInputRequest(UserInputRequest{
		Question: "Sign in",
		Kind:     "form",
		Fields: []UserInputField{
			{Key: "username", Label: "Username", Kind: "text", Placeholder: "you@example.com"},
			{Key: "password", Label: "Password", Kind: "secret", Target: UserInputTarget{Tool: "acme_login", Path: "/password"}},
			{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"machine", "session"}},
		},
	}))

	// Two secrets must bind to distinct paths.
	err := validateUserInputRequest(UserInputRequest{
		Question: "Creds",
		Kind:     "form",
		Fields: []UserInputField{
			{Key: "api_key", Label: "Key", Kind: "secret", Target: UserInputTarget{Tool: "api", Path: "/headers/Auth"}},
			{Key: "api_secret", Label: "Secret", Kind: "secret", Target: UserInputTarget{Tool: "api", Path: "/headers/Auth"}},
		},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate secret target path")

	// Cap at maxFormFields.
	tooMany := make([]UserInputField, maxFormFields+1)
	for i := range tooMany {
		tooMany[i] = UserInputField{Key: fmt.Sprintf("f%d", i), Label: "F", Kind: "text"}
	}
	require.Error(t, validateUserInputRequest(UserInputRequest{Question: "Q", Kind: "form", Fields: tooMany}))
}

func TestRegisterUserInputForm(t *testing.T) {
	registry := NewRegistry()
	var got UserInputRequest
	require.NoError(t, RegisterUserInput(registry, func(_ context.Context, req UserInputRequest) (UserInputResponse, error) {
		got = req
		return UserInputResponse{Form: map[string]FieldResponse{
			"username": {Answer: "alice"},
			"password": {SecretRef: "mods-secret://abc"},
		}}, nil
	}))
	out, err := registry.Call(context.Background(), UserInputToolName, json.RawMessage(`{
		"question":"Sign in","kind":"form",
		"fields":[
			{"key":"username","label":"Username","kind":"text"},
			{"key":"password","label":"Password","kind":"secret","target":{"tool":"acme_login","path":"/password"}}
		]
	}`))
	require.NoError(t, err)
	require.Equal(t, "form", got.Kind)
	require.Len(t, got.Fields, 2)
	require.JSONEq(t, `{"form":{"username":{"answer":"alice"},"password":{"secret_ref":"mods-secret://abc"}}}`, out)
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

func TestValidateSecretEnvReservesStableLocationNames(t *testing.T) {
	require.NoError(t, validateSecretEnv(map[string]string{"DB_PASSWORD": "ref"}))
	require.Error(t, validateSecretEnv(map[string]string{"SystemRoot": "ref"}), "stable location names must stay reserved so classification matches the child shell")
	require.Error(t, validateSecretEnv(map[string]string{"temp": "ref"}))
}
