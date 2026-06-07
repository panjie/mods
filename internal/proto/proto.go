// Package proto shared protocol.
package proto

import (
	"strings"
)

// StripSchema removes descriptive keys (description, title, examples, default)
// from a JSON Schema properties map. Some LLM providers reject these keys in
// tool input schemas, even though they are valid in JSON Schema.
func StripSchema(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		if stripSchemaKeys[k] {
			continue
		}
		if k == "properties" || k == "items" {
			if nested, ok := v.(map[string]any); ok {
				out[k] = StripSchema(nested)
				continue
			}
		}
		m, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		cleaned := make(map[string]any, len(m))
		for mk, mv := range m {
			if stripSchemaKeys[mk] {
				continue
			}
			if mk == "properties" || mk == "items" {
				if nested, ok := mv.(map[string]any); ok {
					cleaned[mk] = StripSchema(nested)
					continue
				}
			}
			cleaned[mk] = mv
		}
		out[k] = cleaned
	}
	return out
}

var stripSchemaKeys = map[string]bool{
	"description": true,
	"title":       true,
	"examples":    true,
	"default":     true,
}

// Roles.
const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// Image represents an image attachment in a message.
type Image struct {
	Data     []byte // raw image bytes
	MimeType string // e.g., "image/png", "image/jpeg"
}

// Chunk is a streaming chunk of text.
type Chunk struct {
	Content string
	Thought string // reasoning/thinking content from the model
}

// ToolCallStatus is the status of a tool call.
type ToolCallStatus struct {
	Name      string
	Arguments []byte
	Err       error
}

// Message is a message in the conversation.
type Message struct {
	Role      string
	Content   string
	Images    []Image
	ToolCalls []ToolCall
}

// ToolCall is a tool call in a message.
type ToolCall struct {
	ID       string
	Function Function
	IsError  bool
}

// Function is the function signature of a tool call.
type Function struct {
	Name      string
	Arguments []byte
}

// ToolSpec is a provider-neutral function/tool definition.
type ToolSpec struct {
	Name        string
	Description string
	InputSchema map[string]any
}

// Request is a chat request.
type Request struct {
	Messages       []Message
	API            string
	Model          string
	User           string
	Tools          []ToolSpec
	Temperature    *float64
	TopP           *float64
	TopK           *int64
	Stop           []string
	MaxTokens      *int64
	ResponseFormat *string
	ToolCaller     func(name string, data []byte) (string, error)
}

// Conversation is a conversation.
type Conversation []Message

func (cc Conversation) String() string {
	var sb strings.Builder
	for _, msg := range cc {
		if msg.Content == "" {
			continue
		}
		switch msg.Role {
		case RoleSystem:
			sb.WriteString("**System**: ")
		case RoleUser:
			sb.WriteString("**User**: ")
		case RoleTool:
			continue
		case RoleAssistant:
			sb.WriteString("**Assistant**: ")
		}
		sb.WriteString(msg.Content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}
