package cohere

import (
	cohere "github.com/cohere-ai/cohere-go/v2"
	"github.com/panjie/mods/internal/proto"
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
	// Cohere's ChatStreamRequest carries the user's current turn separately
	// from the chat history. The previous implementation assumed the last
	// message was always a USER role and dereferenced m.User directly,
	// which panicked when a trailing SYSTEM message (planning prompt
	// injection) or trailing CHATBOT message (cache replay across
	// providers) reached this function. lastMessageContent extracts the
	// text from whichever role field is populated so the request still
	// proceeds without a nil-deref.
	message = lastMessageContent(messages[len(messages)-1])
	return history, message
}

// lastMessageContent returns the populated message text for whichever
// role field is set on m. It returns the empty string when m is nil or
// when none of the role fields are populated; callers translate that
// into an empty Cohere "current turn" rather than panicking.
func lastMessageContent(m *cohere.Message) string {
	switch {
	case m == nil:
		return ""
	case m.User != nil:
		return m.User.Message
	case m.System != nil:
		return m.System.Message
	case m.Chatbot != nil:
		return m.Chatbot.Message
	}
	return ""
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
