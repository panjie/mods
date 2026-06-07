package main

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
)

func lastPrompt(messages []proto.Message) string {
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

func firstLine(s string) string {
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
	name  string
	args  []byte
	label string
	resp  chan bool
}

type toolReviewStartMsg struct {
	item toolReviewItem
}

type cacheDetailsMsg struct {
	WriteID, Title, ReadID, API, Model string
}

type stdinImageInput struct {
	data []byte
}
