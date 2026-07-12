package app

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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
		handled, _ := manager.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		require.True(t, handled)
	}
	handled, _ := manager.handleKey(tea.KeyMsg{Type: tea.KeyEnter})
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
	styles := makeStyles(lipgloss.NewRenderer(nil)).Interaction

	before := strings.Split(manager.render(80, styles), "\n")
	require.Contains(t, before[0], "AUTHENTICATION REQUIRED")
	require.Contains(t, strings.Join(before, "\n"), "Command")
	require.Contains(t, strings.Join(before, "\n"), "sudo rm /usr/local/bin/mods")
	helpBefore := lineContaining(before, "Submit")
	helpLineBefore := lineIndexContaining(before, "Submit")
	require.Contains(t, helpBefore, "Submit")

	manager.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	after := strings.Split(manager.render(80, styles), "\n")
	helpAfter := lineContaining(after, "Submit")
	helpLineAfter := lineIndexContaining(after, "Submit")
	require.Equal(t, helpLineBefore, helpLineAfter, "typing must not push the action row downward")
	require.Equal(t, strings.Index(helpBefore, "Enter"), strings.Index(helpAfter, "Enter"))
	require.Contains(t, helpAfter, "Submit")
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
