package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestUserInputManagerTextRoundTrip(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	start := manager.startSession()

	type result struct {
		resp toolregistry.UserInputResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := manager.request(context.Background(), toolregistry.UserInputRequest{Question: "Database?", Kind: "text"})
		done <- result{resp: resp, err: err}
	}()
	msg := start()
	manager.handleStartMsg(msg.(userInputStartMsg))
	for _, r := range "production" {
		handled, _ := manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		require.True(t, handled)
	}
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "production", got.resp.Answer)
	case <-time.After(time.Second):
		t.Fatal("input request did not complete")
	}
}

func TestUserInputUnavailableInRawMode(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	cfg.Raw = true
	manager := newUserInputManager(&cfg)
	_, err := manager.request(context.Background(), toolregistry.UserInputRequest{Question: "Q", Kind: "text"})
	require.ErrorIs(t, err, errUserInputUnavailable)
}

func TestUserInputResetUnblocksPendingRequest(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })

	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	_ = manager.startSession()

	done := make(chan error, 1)
	go func() {
		_, err := manager.request(context.Background(), toolregistry.UserInputRequest{
			Question: "Continue?",
			Kind:     "text",
		})
		done <- err
	}()

	ch, _ := manager.snapshotSession()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("input request was not queued")
	}
	manager.reset()

	select {
	case err := <-done:
		require.ErrorIs(t, err, errUserInputUnavailable)
	case <-time.After(time.Second):
		t.Fatal("pending input request was not released by reset")
	}
}

func TestSecretUseApprovalIgnoresReviewNever(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	reviewer := &toolReviewer{
		reviewMode: ReviewNever,
		reviewChan: make(chan toolReviewItem, 1),
	}
	done := make(chan error, 1)
	go func() {
		done <- reviewer.requestSecretApproval(context.Background(), "db_query", []byte(`{"password":"mods-secret://ref"}`))
	}()
	item := <-reviewer.reviewChan
	require.Empty(t, item.candidateRules)
	require.Contains(t, item.summary, "protected credential")
	item.resp <- reviewResponse{approved: true}
	require.NoError(t, <-done)
}

func TestSecretInputRenderKeepsAlignmentAndHelpStable(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Password:",
			Kind:     "secret",
		},
		display: userInputDisplay{
			title: "Authentication required", tone: interactionToneDanger,
			headline: "sudo needs elevated privileges",
			rows:     []interactionRow{{Label: "Command", Value: "sudo rm /usr/local/bin/mods"}},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction

	before := strings.Split(manager.render(80, styles), "\n")
	require.Contains(t, before[0], "AUTHENTICATION REQUIRED")
	require.Contains(t, strings.Join(before, "\n"), "Command")
	require.Contains(t, strings.Join(before, "\n"), "sudo rm /usr/local/bin/mods")
	helpBefore := lineContaining(before, "Submit")
	helpLineBefore := lineIndexContaining(before, "Submit")
	require.Contains(t, helpBefore, "Submit")

	manager.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	after := strings.Split(manager.render(80, styles), "\n")
	helpAfter := lineContaining(after, "Submit")
	helpLineAfter := lineIndexContaining(after, "Submit")
	require.Equal(t, helpLineBefore, helpLineAfter, "typing must not push the action row downward")
	require.Equal(t, strings.Index(helpBefore, "Enter"), strings.Index(helpAfter, "Enter"))
	require.Contains(t, helpAfter, "Submit")
}

func TestUserInputManagerFormRoundTrip(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	start := manager.startSession()

	req := toolregistry.UserInputRequest{
		Question: "Sign in",
		Kind:     "form",
		Fields: []toolregistry.UserInputField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret"},
			{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"machine", "session"}},
		},
	}

	type result struct {
		values map[string]string
		err    error
	}
	done := make(chan result, 1)
	go func() {
		values, err := manager.requestForm(context.Background(), req)
		done <- result{values: values, err: err}
	}()
	msg := start()
	manager.handleStartMsg(msg.(userInputStartMsg))

	// Field 0 (text): type "alice", then Tab to move to next field.
	for _, r := range "alice" {
		handled, _ := manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		require.True(t, handled)
	}
	require.Equal(t, 0, manager.focus)
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.True(t, handled)
	require.Equal(t, 1, manager.focus)

	// Field 1 (secret): type "hunter2", then Tab.
	for _, r := range "hunter2" {
		handled, _ := manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
		require.True(t, handled)
	}
	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.True(t, handled)
	require.Equal(t, 2, manager.focus)

	// Field 2 (select): default is "machine". Tab back to field 0 to verify wrap+wrap.
	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.True(t, handled)
	require.Equal(t, 0, manager.focus)
	// Shift+Tab should wrap back to the last field.
	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.True(t, handled)
	require.Equal(t, 2, manager.focus)

	// Submit.
	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "alice", got.values["username"])
		require.Equal(t, "hunter2", got.values["password"])
		require.Equal(t, "machine", got.values["scope"])
	case <-time.After(time.Second):
		t.Fatal("form request did not complete")
	}
}

func TestFormEnterBlockedWhenRequiredFieldEmpty(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	start := manager.startSession()

	req := toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret"},
		},
	}
	done := make(chan struct {
		values map[string]string
		err    error
	}, 1)
	go func() {
		values, err := manager.requestForm(context.Background(), req)
		done <- struct {
			values map[string]string
			err    error
		}{values: values, err: err}
	}()
	manager.handleStartMsg(start().(userInputStartMsg))

	// Only fill username; password is still empty.
	for _, r := range "alice" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	// Enter should be swallowed because password is empty.
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.True(t, manager.isPending(), "form must remain pending while a field is empty")

	// Now fill the password and submit.
	for _, r := range "hunter2" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "alice", got.values["username"])
		require.Equal(t, "hunter2", got.values["password"])
	case <-time.After(time.Second):
		t.Fatal("form was not submitted after filling required fields")
	}
}

func TestFormEscCancels(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	start := manager.startSession()

	req := toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{{Key: "username", Label: "Username", Kind: "text"}},
	}
	done := make(chan error, 1)
	go func() {
		_, err := manager.requestForm(context.Background(), req)
		done <- err
	}()
	manager.handleStartMsg(start().(userInputStartMsg))

	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.True(t, handled)

	select {
	case err := <-done:
		require.ErrorContains(t, err, "user canceled input")
	case <-time.After(time.Second):
		t.Fatal("esc did not cancel form")
	}
}

func TestFormRendersAllFieldLabels(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Sign in", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "Username", Kind: "text"},
				{Key: "password", Label: "Password", Kind: "secret"},
				{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"machine", "session"}},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction
	view := manager.render(80, styles)

	// All three labels render verbatim, plus the Move/Submit/Cancel action row.
	for _, label := range []string{"Username", "Password", "Scope", "Move", "Submit", "Cancel"} {
		require.Contains(t, view, label, "expected %q in form view", label)
	}
	// The headline is the form question.
	require.Contains(t, view, "Sign in")
	// Default selected option for the select field is visible.
	require.Contains(t, view, "machine")
}

func TestFormSelectFieldArrowKeysChangeValue(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Pick", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"machine", "session", "cloud"}},
			},
		},
		resp: make(chan userInputResult, 1),
	}})

	require.Equal(t, 0, manager.formFields[0].selected)
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 1, manager.formFields[0].selected)
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 2, manager.formFields[0].selected)
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyRight})
	require.Equal(t, 0, manager.formFields[0].selected, "right wraps to first option")
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	require.Equal(t, 2, manager.formFields[0].selected, "left wraps to last option")
}

func TestFormUnfocusedSecretIsMasked(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	cfg := defaultConfig()
	manager := newUserInputManager(&cfg)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Sign in", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "Username", Kind: "text"},
				{Key: "password", Label: "Password", Kind: "secret"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	// Focus the secret field (index 1), type, then move focus back to text.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	for _, r := range "hunter2" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	require.Equal(t, 0, manager.focus)

	styles := makeStyles(true).Interaction
	view := manager.render(80, styles)
	// Non-focused secret value must not appear verbatim.
	require.NotContains(t, view, "hunter2")
	// But it must render one bullet per rune.
	require.Contains(t, view, strings.Repeat("•", len("hunter2")))
}

func TestFormRealCursorPropagatesToModsView(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Sign in", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "Username", Kind: "text"},
				{Key: "password", Label: "Password", Kind: "secret"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	m := &Mods{
		Config:       &Config{},
		Styles:       makeStyles(true),
		state:        requestState,
		width:        80,
		userInput:    manager,
		reviewer:     &toolReviewer{},
		contentMutex: &sync.Mutex{},
	}
	view := m.View()
	require.NotNil(t, view.Cursor, "focused text field must produce a cursor")

	// Tab into the secret field; cursor remains visible.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	moved := m.View()
	require.NotNil(t, moved.Cursor, "focused secret field must produce a cursor")
	require.NotEqual(t, view.Cursor.Position, moved.Cursor.Position, "cursor must track focus")

	manager.pending = false
	require.Nil(t, m.View().Cursor)
}

func TestHandleFormInputRejectsSecretsInPlanMode(t *testing.T) {
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Kind: toolregistry.ToolKindShell,
		Spec: proto.ToolSpec{Name: "shell_run"},
		Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call:         noopToolCall,
	}))
	m := &Mods{
		Config:              &Config{Plan: true},
		currentToolRegistry: registry,
		secrets:             secrets.New(),
	}
	_, err := m.handleFormInput(context.Background(), toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/PASSWORD"}},
		},
	})
	require.ErrorContains(t, err, "plan mode")
}

func TestHandleFormInputRejectsUnknownSecretTarget(t *testing.T) {
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Kind:         toolregistry.ToolKindBuiltin,
		Spec:         proto.ToolSpec{Name: "shell_run"},
		Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call:         noopToolCall,
	}))
	m := &Mods{
		Config:              &Config{},
		currentToolRegistry: registry,
		secrets:             secrets.New(),
	}
	_, err := m.handleFormInput(context.Background(), toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{
			{Key: "password", Label: "Password", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/PASSWORD"}},
		},
	})
	require.ErrorContains(t, err, "secret target must be an available MCP or shell tool")
}

func TestHandleFormInputStoresPerFieldSecrets(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })

	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Kind:         toolregistry.ToolKindShell,
		Spec:         proto.ToolSpec{Name: "shell_run"},
		Capabilities: toolregistry.ToolCapabilities{Mutable: true, ShellExecution: true},
		Call:         noopToolCall,
	}))
	store := secrets.New()
	manager := newUserInputManager(&Config{})
	m := &Mods{
		Config:              &Config{},
		currentToolRegistry: registry,
		secrets:             store,
		userInput:           manager,
	}
	start := manager.startSession()

	req := toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{
			{Key: "username", Label: "Username", Kind: "text"},
			{Key: "password", Label: "Password", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/PASSWORD"}},
			{Key: "token", Label: "Token", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/TOKEN"}},
		},
	}

	type result struct {
		resp toolregistry.UserInputResponse
		err  error
	}
	done := make(chan result, 1)
	go func() {
		resp, err := m.handleFormInput(context.Background(), req)
		done <- result{resp: resp, err: err}
	}()
	manager.handleStartMsg(start().(userInputStartMsg))

	// username
	for _, r := range "alice" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	// password
	for _, r := range "hunter2" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	// token
	for _, r := range "tok-123" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "alice", got.resp.Form["username"].Answer)
		require.NotEmpty(t, got.resp.Form["password"].SecretRef)
		require.NotEmpty(t, got.resp.Form["token"].SecretRef)
		require.NotEqual(t, got.resp.Form["password"].SecretRef, got.resp.Form["token"].SecretRef, "two secrets must produce distinct refs")
		// Both refs resolve back to their bound values at the bound paths.
		for path, want := range map[string]string{
			"/secret_env/PASSWORD": "hunter2",
			"/secret_env/TOKEN":    "tok-123",
		} {
			input := []byte(`{"secret_env":{"PASSWORD":"` + got.resp.Form["password"].SecretRef + `","TOKEN":"` + got.resp.Form["token"].SecretRef + `"}}`)
			resolved, used, err := store.Resolve("shell_run", input)
			require.NoError(t, err)
			require.True(t, used, "ref at %s must resolve", path)
			require.Contains(t, string(resolved), want)
		}
	case <-time.After(time.Second):
		t.Fatal("handleFormInput did not complete")
	}
}


func TestUserInputRealCursorPropagatesToModsView(t *testing.T) {
	for _, kind := range []string{"text", "secret"} {
		t.Run(kind, func(t *testing.T) {
			manager := newUserInputManager(&Config{})
			manager.handleStartMsg(userInputStartMsg{item: userInputItem{
				req:  toolregistry.UserInputRequest{Kind: kind, Question: "Value?"},
				resp: make(chan userInputResult, 1),
			}})
			m := &Mods{
				Config:       &Config{},
				Styles:       makeStyles(true),
				state:        requestState,
				width:        60,
				userInput:    manager,
				reviewer:     &toolReviewer{},
				contentMutex: &sync.Mutex{},
			}
			view := m.View()
			require.NotNil(t, view.Cursor)

			if kind == "secret" {
				manager.secret.SetValue("秘密")
				manager.secret.CursorEnd()
			} else {
				manager.text.SetValue("中文\ninput")
				manager.text.CursorEnd()
			}
			moved := m.View()
			require.NotNil(t, moved.Cursor)
			require.NotEqual(t, view.Cursor.Position, moved.Cursor.Position)

			manager.pending = false
			require.Nil(t, m.View().Cursor)
		})
	}
}

func lineIndexContaining(lines []string, value string) int {
	for i, line := range lines {
		if strings.Contains(line, value) {
			return i
		}
	}
	return -1
}

func lineContaining(lines []string, value string) string {
	for _, line := range lines {
		if strings.Contains(line, value) {
			return line
		}
	}
	return ""
}
