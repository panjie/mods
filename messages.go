package main

import (
	"strings"

	"github.com/charmbracelet/mods/internal/proto"
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
