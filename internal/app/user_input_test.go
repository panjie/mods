package app

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/secrets"
	toolregistry "github.com/panjie/mods/internal/tools"
	"github.com/panjie/mods/internal/ui"
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

func TestUserInputManagerMultiselectRoundTrip(t *testing.T) {
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
		resp, err := manager.request(context.Background(), toolregistry.UserInputRequest{
			Question: "What should run?",
			Kind:     "multiselect",
			Options:  []string{"tests", "lint", "docs"},
		})
		done <- result{resp: resp, err: err}
	}()
	manager.handleStartMsg(start().(userInputStartMsg))

	// Empty selections cannot be submitted.
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.True(t, manager.isPending())

	// Select docs first, then tests. Results must retain original option order.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyUp})
	require.Equal(t, 3, manager.selected, "up wraps to the last real option")
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 0, manager.selected, "tab wraps to the select-all row")
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 1, manager.selected, "tab advances to the first real option")
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})

	// Toggle tests off and back on to cover deselection.
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.False(t, manager.checked[0])
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.True(t, manager.checked[0])
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})

	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, []string{"tests", "docs"}, got.resp.Answers)
		require.Empty(t, got.resp.Answer)
	case <-time.After(time.Second):
		t.Fatal("multiselect request did not complete")
	}
}

func TestUserInputMultiselectRenderAndCancel(t *testing.T) {
	manager := newUserInputManager(&Config{})
	response := make(chan userInputResult, 1)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Choose checks",
			Kind:     "multiselect",
			Options:  []string{"tests", "lint"},
		},
		resp: response,
	}})

	// Space on the virtual first row selects and clears every real option.
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Equal(t, []bool{true, true}, manager.checked)
	allView := ansi.Strip(manager.render(80, makeStyles(true).Interaction))
	require.Contains(t, allView, "[x] Select all")
	require.Contains(t, allView, "[x] tests")
	require.Contains(t, allView, "[x] lint")
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Equal(t, []bool{false, false}, manager.checked)

	// A partial selection renders the select-all row indeterminately.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, manager.selected)
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	partialView := ansi.Strip(manager.render(80, makeStyles(true).Interaction))
	require.Contains(t, partialView, "[-] Select all")
	require.Contains(t, partialView, "[x] tests")
	require.Contains(t, partialView, "[ ] lint")

	// The shortcut works from a real option: partial -> all -> none.
	manager.handleKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	require.Equal(t, []bool{true, true}, manager.checked)
	manager.handleKey(tea.KeyPressMsg{Code: 'A', Text: "A"})
	require.Equal(t, []bool{false, false}, manager.checked)

	view := ansi.Strip(manager.render(80, makeStyles(true).Interaction))
	require.Contains(t, view, "[ ] Select all")
	require.Contains(t, view, "[ ] tests")
	require.Contains(t, view, "[ ] lint")
	require.Contains(t, view, "Space")
	require.Contains(t, view, "Toggle")
	require.Contains(t, view, "A Toggle all")
	lines := strings.Split(view, "\n")
	selectAllLine := lineIndexContaining(lines, "Select all")
	testsLine := lineIndexContaining(lines, "tests")
	lintLine := lineIndexContaining(lines, "lint")
	require.Greater(t, selectAllLine, -1)
	require.Equal(t, selectAllLine+1, testsLine, "select-all row must precede real choices")
	require.Equal(t, testsLine+1, lintLine, "multiselect choices must be stacked vertically")

	// Clearing all restores the empty-submit guard.
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.True(t, manager.isPending())

	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	require.True(t, handled)
	select {
	case got := <-response:
		require.ErrorContains(t, got.err, "user canceled input")
	case <-time.After(time.Second):
		t.Fatal("esc did not cancel multiselect")
	}
}

func TestUserInputManagerSelectRoundTrip(t *testing.T) {
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
		resp, err := manager.request(context.Background(), toolregistry.UserInputRequest{
			Question: "How should the remote changes be integrated?",
			Kind:     "select",
			Options:  []string{"merge", "rebase", "stash"},
		})
		done <- result{resp: resp, err: err}
	}()
	manager.handleStartMsg(start().(userInputStartMsg))

	// The first option is checked by default.
	require.Equal(t, []bool{true, false, false}, manager.checked)

	// Space selects the highlighted option and unchecks the previous one.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	require.Equal(t, 1, manager.selected)
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Equal(t, []bool{false, true, false}, manager.checked)

	// Space on the already-checked option keeps it selected.
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	require.Equal(t, []bool{false, true, false}, manager.checked)

	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	select {
	case got := <-done:
		require.NoError(t, got.err)
		require.Equal(t, "rebase", got.resp.Answer)
	case <-time.After(time.Second):
		t.Fatal("select request did not complete")
	}
}

func TestUserInputSelectRendersStackedCheckboxesWithinWidth(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "How should the remote changes be integrated?",
			Kind:     "select",
			Options: []string{
				"git pull (merge) - create a merge commit",
				"git rebase - replay my commit on top of remote",
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction
	for _, width := range []int{40, 60, 100} {
		lines := strings.Split(manager.render(width, styles), "\n")
		for _, line := range lines {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: line %q overflows", width, line)
		}
	}
	plain := ansi.Strip(manager.render(100, styles))
	require.Contains(t, plain, "(•) git pull (merge) - create a merge commit")
	require.Contains(t, plain, "( ) git rebase - replay my commit on top of remote")
	require.Contains(t, plain, "Confirm")
	require.NotContains(t, plain, "Space", "radio select must not advertise the multiselect toggle key")
	lines := strings.Split(plain, "\n")
	mergeLine := lineIndexContaining(lines, "git pull")
	rebaseLine := lineIndexContaining(lines, "git rebase")
	require.Greater(t, mergeLine, -1)
	require.Less(t, mergeLine, rebaseLine, "select choices must be stacked vertically")

	// Narrow widths wrap long labels onto indented continuation lines.
	narrow := strings.Split(ansi.Strip(manager.render(40, styles)), "\n")
	narrowMergeLine := lineIndexContaining(narrow, "git pull")
	require.Greater(t, narrowMergeLine, -1)
	require.Less(t, narrowMergeLine+1, len(narrow))
	continuation := narrow[narrowMergeLine+1]
	require.Contains(t, continuation, "commit", "long labels must wrap onto a continuation line")
	require.True(t, strings.HasPrefix(continuation, "┃      "),
		"continuation must align under the label start: %q", continuation)
}

func TestUserInputSelectRendersNerdGlyphs(t *testing.T) {
	cfg := defaultConfig()
	cfg.NerdFontGlyphs = true
	manager := newUserInputManager(&cfg)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "How should the remote changes be integrated?",
			Kind:     "select",
			Options: []string{
				"git pull (merge) - create a merge commit",
				"git rebase - replay my commit on top of remote for linear history",
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction
	for _, width := range []int{40, 60, 100} {
		for _, line := range strings.Split(manager.render(width, styles), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: line %q overflows", width, line)
		}
	}
	plain := ansi.Strip(manager.render(100, styles))
	require.Contains(t, plain, ui.NerdRadioOn+" git pull (merge) - create a merge commit")
	require.Contains(t, plain, ui.NerdRadioOff+" git rebase - replay my commit on top of remote")
	require.NotContains(t, plain, "(•)")
	require.NotContains(t, plain, "[x]")
}

func TestUserInputMultiselectRendersNerdGlyphs(t *testing.T) {
	cfg := defaultConfig()
	cfg.NerdFontGlyphs = true
	manager := newUserInputManager(&cfg)
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Choose checks",
			Kind:     "multiselect",
			Options:  []string{"tests", "lint"},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction

	plain := ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, ui.NerdCheckOff+" Select all")
	require.Contains(t, plain, ui.NerdCheckOff+" tests")
	require.Contains(t, plain, ui.NerdCheckOff+" lint")

	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	plain = ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, ui.NerdCheckOn+" Select all")
	require.Contains(t, plain, ui.NerdCheckOn+" tests")
	require.Contains(t, plain, ui.NerdCheckOn+" lint")

	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyDown})
	manager.handleKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	plain = ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, ui.NerdCheckMid+" Select all")
	require.Contains(t, plain, ui.NerdCheckOn+" tests")
	require.Contains(t, plain, ui.NerdCheckOff+" lint")
	require.NotContains(t, plain, "[x]")
	require.NotContains(t, plain, "[ ]")
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

// A secret reference is authorized at input time by its user-bound target, so
// the downstream call needs no per-use approval; Resolve still enforces the
// binding and output stays redacted.
func TestSecretRefCallNeedsNoPerUseApproval(t *testing.T) {
	cfg := defaultConfig()
	cfg.ReviewMode = ReviewNever
	cfg.MCPTimeout = time.Second
	store := secrets.New()
	ref, err := store.Put("hunter2", secrets.Target{Tool: "lookup", Path: "/token"})
	require.NoError(t, err)
	m := &Mods{
		Config:   &cfg,
		ctx:      context.Background(),
		reviewer: newToolReviewer(&cfg),
		secrets:  store,
	}
	registry := toolregistry.NewRegistry()
	require.NoError(t, registry.Register(toolregistry.Tool{
		Spec: proto.ToolSpec{
			Name: "lookup",
			InputSchema: map[string]any{
				"type": "object", "required": []string{"token"},
				"properties": map[string]any{"token": map[string]any{"type": "string"}},
			},
		},
		Capabilities: toolregistry.ToolCapabilities{ReadOnly: true},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			return "received " + string(data), nil
		},
	}))

	out, err := m.toolCaller(registry, &cfg)(proto.ToolCallRequest{
		ID: "call_secret", Index: 1, Total: 1, Name: "lookup",
		Arguments: []byte(`{"token":"` + ref + `"}`),
	})
	require.NoError(t, err)
	require.Contains(t, out, "[REDACTED]")
	require.NotContains(t, out, "hunter2")
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

func TestFormFocusedSecretPlaceholderAlignsWithUnfocusedTextValue(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "请输入您的OA登录凭证", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "OA用户名", Kind: "text"},
				{Key: "password", Label: "OA密码", Kind: "secret"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})

	for _, r := range "panjie" {
		manager.handleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})

	lines := strings.Split(ansi.Strip(manager.render(80, makeStyles(true).Interaction)), "\n")
	usernameLine := lineContaining(lines, "OA用户名")
	passwordLine := lineContaining(lines, "OA密码")
	require.NotEmpty(t, usernameLine)
	require.NotEmpty(t, passwordLine)

	require.Equal(t, visualColumn(usernameLine, "panjie"), visualColumn(passwordLine, "Enter secret"))
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

func TestFormFocusedInputHasNoBackgroundPill(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "OA 会话已过期，请输入登录信息：", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "OA 用户名", Kind: "text"},
				{Key: "password", Label: "OA 密码", Kind: "secret"},
				{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"machine", "session"}},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	manager.handleKey(tea.KeyPressMsg{Code: 'p', Text: "p"})

	styles := makeStyles(true).Interaction
	view := manager.renderView(80, styles)
	require.NotNil(t, view.Cursor)
	line := strings.Split(view.Content, "\n")[view.Cursor.Y]

	// The focused row must not paint the input background: no Surface pill
	// padding cell right after the marker and no cursor-line black fill.
	require.NotContains(t, line, "48;2;48;43;72")
	require.NotContains(t, line, "\x1b[40m")
	stripped := ansi.Strip(line)
	require.Contains(t, stripped, "OA 用户名 › p")
	require.Equal(t, visualColumn(stripped, "p")+1, view.Cursor.X, "cursor must sit right after the typed character")

	// Focused select fields render the same marker+plain-value shape.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	selectView := manager.renderView(80, styles)
	for _, line := range strings.Split(selectView.Content, "\n") {
		if strings.Contains(ansi.Strip(line), "machine") {
			require.NotContains(t, line, "48;2;48;43;72", "focused select row must not paint the input background")
		}
	}
}

func TestFormCursorTracksVisibleTailWhenViewOverflowsTerminal(t *testing.T) {
	oldTTY := IsInputTTY
	IsInputTTY = func() bool { return true }
	t.Cleanup(func() { IsInputTTY = oldTTY })
	oldOut := IsOutputTTY
	IsOutputTTY = func() bool { return true }
	t.Cleanup(func() { IsOutputTTY = oldOut })

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
	newOverflowMods := func(height int) *Mods {
		return &Mods{
			Config: &Config{},
			Styles: makeStyles(true),
			state:  responseState,
			width:  80,
			height: height,
			outputRenderer: outputRenderer{
				glamOutput: strings.Repeat("history\n", 18) + "tail",
			},
			userInput:    manager,
			reviewer:     &toolReviewer{},
			contentMutex: &sync.Mutex{},
		}
	}

	// With room to spare the cursor keeps its view-relative row.
	view := newOverflowMods(40).View()
	require.NotNil(t, view.Cursor)
	lines := strings.Split(view.Content, "\n")
	usernameRow := lineIndexContaining(lines, "Username")
	require.GreaterOrEqual(t, usernameRow, 0)
	require.Equal(t, usernameRow, view.Cursor.Y)

	// When the view is taller than the terminal, the renderer keeps only the
	// last height lines, so the cursor must shift up by the dropped line count.
	overflow := newOverflowMods(20)
	view = overflow.View()
	require.NotNil(t, view.Cursor)
	lines = strings.Split(view.Content, "\n")
	usernameRow = lineIndexContaining(lines, "Username")
	dropped := len(lines) - 20
	require.Greater(t, dropped, 0)
	require.Equal(t, usernameRow-dropped, view.Cursor.Y)
	require.GreaterOrEqual(t, view.Cursor.Y, 0)
	require.Less(t, view.Cursor.Y, 20)
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

func TestUserInputTitlesAndHints(t *testing.T) {
	styles := makeStyles(true).Interaction
	newManager := func(req toolregistry.UserInputRequest) *userInputManager {
		manager := newUserInputManager(&Config{})
		manager.handleStartMsg(userInputStartMsg{item: userInputItem{
			req:  req,
			resp: make(chan userInputResult, 1),
		}})
		return manager
	}

	// Single-line text: INPUT title, no Ctrl+J hint.
	plain := ansi.Strip(newManager(toolregistry.UserInputRequest{Question: "Branch?", Kind: "text"}).render(80, styles))
	require.Contains(t, plain, "INPUT")
	require.NotContains(t, plain, "Ctrl+J")

	// Multiline text advertises Ctrl+J.
	plain = ansi.Strip(newManager(toolregistry.UserInputRequest{Question: "Branch?", Kind: "text", Multiline: true}).render(80, styles))
	require.Contains(t, plain, "Ctrl+J")

	// Secret: CREDENTIALS title.
	plain = ansi.Strip(newManager(toolregistry.UserInputRequest{
		Question: "Token?", Kind: "secret",
		Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/TOKEN"},
	}).render(80, styles))
	require.Contains(t, plain, "CREDENTIALS")

	// Form containing a secret field also gets the CREDENTIALS title.
	plain = ansi.Strip(newManager(toolregistry.UserInputRequest{
		Question: "Sign in", Kind: "form",
		Fields: []toolregistry.UserInputField{
			{Key: "u", Label: "User", Kind: "text"},
			{Key: "p", Label: "Pass", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/PASS"}},
		},
	}).render(80, styles))
	require.Contains(t, plain, "CREDENTIALS")
}

func TestUserInputHeadlineClamped(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req:  toolregistry.UserInputRequest{Question: strings.Repeat("这是一个特别长的问题描述、", 30), Kind: "text"},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction
	view := manager.render(40, styles)
	lines := strings.Split(view, "\n")
	// Title + at most 2 headline lines + blank + input + blank + actions,
	// all inside the panel border.
	require.LessOrEqual(t, len(lines), 8, "clamped headline must not flood the panel")
	plain := ansi.Strip(view)
	require.Contains(t, plain, "…", "clamped headline must end with an ellipsis")
}

func TestFormStacksOverlongLabels(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "补全信息", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "target", Label: "你想启动什么类型的 Windows 环境", Kind: "text", Placeholder: "如 WSL 或虚拟机"},
				{Key: "logs", Label: "日志", Kind: "text"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction
	for _, width := range []int{40, 60, 80} {
		for _, line := range strings.Split(manager.render(width, styles), "\n") {
			require.LessOrEqual(t, lipgloss.Width(line), width, "width %d: line %q overflows", width, line)
		}
	}

	plainLines := strings.Split(ansi.Strip(manager.render(80, styles)), "\n")
	// The long label renders complete on its own line: never hard-wrapped
	// mid-word like "Windo/ws".
	idx := lineIndexContaining(plainLines, "你想启动什么类型的")
	require.GreaterOrEqual(t, idx, 0)
	require.Contains(t, plainLines[idx], "你想启动什么类型的 Windows 环境")
	// The placeholder indents on the following line, under the label.
	require.Less(t, idx+1, len(plainLines))
	require.Contains(t, plainLines[idx+1], "如 WSL 或虚拟机")
	// The short label stays inline on a single line.
	logsIdx := lineIndexContaining(plainLines, "日志")
	require.GreaterOrEqual(t, logsIdx, 0)
	require.Contains(t, plainLines[logsIdx], "日志")
}

func TestFormDynamicLabelColumnAlignsPlaceholders(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Connect", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "host", Label: "Host", Kind: "text", Placeholder: "alpha.example.com"},
				{Key: "port", Label: "Port", Kind: "text", Placeholder: "9999"},
				{Key: "name", Label: "Name", Kind: "text"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	// Move focus past the two fields under test so both render unfocused.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, 2, manager.focus)

	lines := strings.Split(ansi.Strip(manager.render(80, makeStyles(true).Interaction)), "\n")
	hostLine := lineContaining(lines, "alpha.example.com")
	portLine := lineContaining(lines, "9999")
	require.NotEmpty(t, hostLine)
	require.NotEmpty(t, portLine)
	require.Equal(t, visualColumn(hostLine, "alpha.example.com"), visualColumn(portLine, "9999"),
		"unfocused values must align in one dynamic label column")
}

func TestFormRequiredFeedback(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Sign in", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "username", Label: "User", Kind: "text"},
				{Key: "password", Label: "Pass", Kind: "secret", Target: toolregistry.UserInputTarget{Tool: "shell_run", Path: "/secret_env/PASS"}},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction

	// Enter with the required username empty is blocked and flags the field.
	handled, _ := manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.True(t, handled)
	require.True(t, manager.isPending())
	require.Equal(t, "username", manager.missingKey)
	plain := ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, "· required")

	// Any subsequent keypress clears the flag.
	handled, _ = manager.handleKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	require.True(t, handled)
	require.Empty(t, manager.missingKey)
	require.NotContains(t, ansi.Strip(manager.render(80, styles)), "· required")

	// Enter now blocks on the still-empty secret field instead.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "password", manager.missingKey)
	require.True(t, manager.isPending())
}

func TestFormContextualHints(t *testing.T) {
	manager := newUserInputManager(&Config{})
	manager.handleStartMsg(userInputStartMsg{item: userInputItem{
		req: toolregistry.UserInputRequest{
			Question: "Deploy", Kind: "form",
			Fields: []toolregistry.UserInputField{
				{Key: "scope", Label: "Scope", Kind: "select", Options: []string{"staging", "prod"}},
				{Key: "notes", Label: "Notes", Kind: "text", Multiline: true},
				{Key: "name", Label: "Name", Kind: "text"},
			},
		},
		resp: make(chan userInputResult, 1),
	}})
	styles := makeStyles(true).Interaction

	// Select field focused: arrow hint only.
	plain := ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, "← →")
	require.NotContains(t, plain, "Ctrl+J")

	// Multiline text focused: Ctrl+J only.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	plain = ansi.Strip(manager.render(80, styles))
	require.Contains(t, plain, "Ctrl+J")
	require.NotContains(t, plain, "← →")

	// Plain text focused: neither.
	manager.handleKey(tea.KeyPressMsg{Code: tea.KeyTab})
	plain = ansi.Strip(manager.render(80, styles))
	require.NotContains(t, plain, "Ctrl+J")
	require.NotContains(t, plain, "← →")
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

func visualColumn(line, value string) int {
	idx := strings.Index(line, value)
	if idx < 0 {
		return -1
	}
	return lipgloss.Width(line[:idx])
}
