package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
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
