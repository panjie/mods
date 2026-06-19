package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
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

// completionOutput a tea.Msg that wraps the content returned from openai.
type completionOutput struct {
	content string
	stream  stream.Stream
	errh    func(error) tea.Msg
}

type toolCallsStartMsg struct {
	stream stream.Stream
	errh   func(error) tea.Msg
}

type toolCallsOutput struct {
	results []proto.ToolCallStatus
	stream  stream.Stream
	errh    func(error) tea.Msg
}

type toolOperationStatusMsg struct {
	content string
	done    bool
	ch      <-chan toolOperationStatusMsg
}

type toolReviewItem struct {
	name        string
	args        []byte
	alwaysRules []Rule
	resp        chan reviewResponse
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
	remember bool
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

type planDeniedMsg struct {
	content string
}

type planModifyMsg struct {
	feedback string
	plan     string
}
