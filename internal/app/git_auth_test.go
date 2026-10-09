package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	debugpkg "github.com/panjie/mods/internal/debug"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestGitAuthPromptRedactsBeforeReturning(t *testing.T) {
	old := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = old })
	cfg := defaultConfig()
	m := &Mods{Config: &cfg, userInput: newUserInputManager(&cfg), secrets: secrets.New()}
	start := m.userInput.startSession()
	defer m.userInput.reset()
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, err := m.handleGitAuthPrompt(context.Background(), toolregistry.GitAuthRequest{Prompt: "Password for 'https://alice:embedded@example.invalid/repo?token=hidden':\x1b[31m", Command: "git fetch https://alice:embedded@example.invalid/repo?token=hidden"})
		done <- result{value, err}
	}()
	m.userInput.handleStartMsg(start().(userInputStartMsg))
	require.Equal(t, "secret", m.userInput.item.req.Kind)
	for _, row := range m.userInput.item.display.rows {
		require.NotContains(t, row.Value, "embedded")
		require.NotContains(t, row.Value, "hidden")
		require.NotContains(t, row.Value, "\x1b")
	}
	m.userInput.secret.SetValue("git-token-123")
	_, _ = m.userInput.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "git-token-123", got.value)
		require.Equal(t, "[REDACTED]", m.secrets.Redact(got.value))
	case <-time.After(time.Second):
		t.Fatal("prompt did not return")
	}
	require.Empty(t, m.userInput.secret.Value())
}

func TestGitAuthInputCancellation(t *testing.T) {
	manager := newUserInputManager(&Config{})
	_ = manager.startSession()
	defer manager.reset()
	ctx, cancel := context.WithCancel(context.Background())
	response := make(chan userInputResult, 1)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{req: toolregistry.UserInputRequest{Kind: "secret"}, resp: response, ctx: ctx, finished: make(chan struct{})}})
	manager.secret.SetValue("discarded-secret")
	watch := manager.watchCancellation()
	cancel()
	msg := watch().(userInputCancelMsg)
	require.NotNil(t, manager.handleCancelMsg(msg))
	require.False(t, manager.isPending())
	require.Empty(t, manager.secret.Value())
	require.ErrorIs(t, (<-response).err, context.Canceled)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{req: toolregistry.UserInputRequest{Kind: "text"}, resp: make(chan userInputResult, 1)}})
	require.Nil(t, manager.handleCancelMsg(msg))
	require.True(t, manager.isPending(), "late cancellation must not dismiss next input")
}

func TestGitAuthUnavailableModes(t *testing.T) {
	old := IsInputTTY
	t.Cleanup(func() { IsInputTTY = old })
	for _, tc := range []struct {
		name              string
		tty, raw, minimal bool
	}{
		{"raw", true, true, false}, {"minimal", true, false, true}, {"no tty", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			IsInputTTY = func() bool { return tc.tty }
			cfg := defaultConfig()
			cfg.Raw = tc.raw
			cfg.Minimal = tc.minimal
			m := &Mods{Config: &cfg, userInput: newUserInputManager(&cfg), secrets: secrets.New()}
			_, err := m.handleGitAuthPrompt(context.Background(), toolregistry.GitAuthRequest{})
			require.True(t, errors.Is(err, errUserInputUnavailable))
		})
	}
}

func TestGitAuthExpiredQueuedInput(t *testing.T) {
	m := testMods(t)
	m.userInput = newUserInputManager(&Config{})
	_ = m.userInput.startSession()
	defer m.userInput.reset()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, cmd := m.Update(userInputStartMsg{item: userInputItem{ctx: ctx, req: toolregistry.UserInputRequest{Kind: "secret"}, resp: make(chan userInputResult, 1)}})
	require.False(t, m.userInput.isPending())
	require.NotNil(t, cmd)
}

func TestGitAuthRuntimeSecretsDoNotReachToolResultsOrDebug(t *testing.T) {
	var log bytes.Buffer
	restore := debugpkg.SetOutputForTest(&log)
	debugpkg.SetEnabled(true)
	t.Cleanup(func() { debugpkg.SetEnabled(false); restore() })
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewNever
	cfg.MCPTimeout = time.Second
	m := &Mods{Config: &cfg, ctx: context.Background(), reviewer: newToolReviewer(&cfg), secrets: secrets.New()}
	secret := "runtime-token\\\"<&>"
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec:         proto.ToolSpec{Name: "auth_test", InputSchema: map[string]any{"type": "object"}},
		Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		Call: func(context.Context, json.RawMessage) (string, error) {
			m.secrets.RememberForRedaction(secret)
			out, _ := json.Marshal(map[string]string{"stdout": secret})
			return string(out), errors.New("failed with " + secret)
		},
	}))
	result, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{Name: "auth_test", Arguments: []byte(`{}`)})
	require.JSONEq(t, `{"stdout":"[REDACTED]"}`, result)
	require.EqualError(t, err, "failed with [REDACTED]")
	require.NotContains(t, log.String(), "runtime-token")
}
