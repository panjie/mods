package cohere

import (
	"github.com/panjie/mods/internal/proto"
	cohere "github.com/cohere-ai/cohere-go/v2"
)

func fromProtoMessages(input []proto.Message) (history []*cohere.Message, message string) {
	if len(input) == 0 {
		return nil, ""
	}
	var messages []*cohere.Message
	for _, msg := range input {
		role := fromProtoRole(msg.Role)
		m := &cohere.Message{Role: role}
		switch role {
		case "SYSTEM":
			m.System = &cohere.ChatMessage{Message: msg.Content}
		case "CHATBOT":
			m.Chatbot = &cohere.ChatMessage{Message: msg.Content}
		default:
			m.User = &cohere.ChatMessage{Message: msg.Content}
		}
		messages = append(messages, m)
	}
	if len(messages) > 1 {
		history = messages[:len(messages)-1]
	}
	message = messages[len(messages)-1].User.Message
	return history, message
}

func toProtoMessages(input []*cohere.Message) []proto.Message {
	var messages []proto.Message
	for _, in := range input {
		switch in.Role {
		case "USER":
			messages = append(messages, proto.Message{
				Role:    proto.RoleUser,
				Content: in.User.Message,
			})
		case "SYSTEM":
			messages = append(messages, proto.Message{
				Role:    proto.RoleSystem,
				Content: in.System.Message,
			})
		case "CHATBOT":
			messages = append(messages, proto.Message{
				Role:    proto.RoleAssistant,
				Content: in.Chatbot.Message,
			})
		case "TOOL":
			// not supported yet
		}
	}
	return messages
}

func fromProtoRole(role string) string {
	switch role {
	case proto.RoleSystem:
		return "SYSTEM"
	case proto.RoleAssistant:
		return "CHATBOT"
	default:
		return "USER"
	}
}
