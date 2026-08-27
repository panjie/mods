package app

import (
	"time"

	"github.com/panjie/mods/internal/proto"
)

func LastPrompt(messages []proto.Message) string {
	var result string
	for _, msg := range messages {
		if msg.Role != proto.RoleUser {
			continue
		}
		if msg.Content == "" {
			continue
		}
		result = msg.Content
	}
	return result
}

// completionInput is a tea.Msg that wraps the content read from stdin.
type completionInput struct {
	content string
}

// terminalProbeTimeoutMsg releases interactive startup when a terminal does
// not answer the background-color query. Terminals that do answer normally
// continue as soon as BackgroundColorMsg arrives.
type terminalProbeTimeoutMsg struct{}

// retryMsg signals that a retryable provider error occurred and the
// completion should be re-attempted after a backoff delay. retry() returns
// this message instead of calling time.Sleep so the Bubble Tea Update loop
// stays responsive (especially to Ctrl+C and other keystrokes) during the
// wait. Update() converts it into a tea.Tick that fires completionInput
// after the requested duration.
type retryMsg struct {
	content string
	wait    time.Duration
}

type toolOperationStatusMsg struct {
	content string
	done    bool
	ch      <-chan toolOperationStatusMsg
}

type toolReviewItem struct {
	name           string
	args           []byte
	candidateRules []Rule
	summary        string
	presentation   reviewPresentation
	resp           chan reviewResponse
}

type toolReviewStartMsg struct {
	item toolReviewItem
}

type userInputStartMsg struct {
	item userInputItem
}

// quitMsg is returned by quit() after its goroutine-safe teardown so the
// model-mutating parts (user input reset) happen in Update, never on a
// command goroutine that races View.
type quitMsg struct{}

type sessionDetailsMsg struct {
	WriteID, Title, ReadID string
	Rules                  []Rule
}

type reviewResponse struct {
	approved bool
}

type stdinImageInput struct {
	data []byte
}
