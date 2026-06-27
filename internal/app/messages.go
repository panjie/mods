package app

import (
	"strings"
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

func lastAssistantContent(messages []proto.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == proto.RoleAssistant && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return ""
}

func FirstLine(s string) string {
	first, _, _ := strings.Cut(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	return first
}

// completionInput is a tea.Msg that wraps the content read from stdin.
type completionInput struct {
	content string
}

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
	resp           chan reviewResponse
}

type toolReviewStartMsg struct {
	item toolReviewItem
}

type cacheDetailsMsg struct {
	WriteID, Title, ReadID, API, Model string
	Rules                              []Rule
}

type reviewResponse struct {
	approved bool
}

type stdinImageInput struct {
	data []byte
}

type planCompleteMsg struct {
	plan string
}

type planApprovedMsg struct {
	plan string
}

type planExecutionStartMsg struct{}

type planDeniedMsg struct {
	content string
}

type planModifyMsg struct {
	feedback string
	plan     string
}
