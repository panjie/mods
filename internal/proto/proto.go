// Package proto shared protocol.
package proto

import (
	"encoding/json"
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

// TokenUsage is provider-neutral token consumption for one or more model
// calls. Providers accumulate usage across tool-call rounds before exposing it
// to the application.
type TokenUsage struct {
	InputTokens  int64
	OutputTokens int64
	TotalTokens  int64
}

// Add accumulates another usage value.
func (u *TokenUsage) Add(other TokenUsage) {
	u.InputTokens += other.InputTokens
	u.OutputTokens += other.OutputTokens
	u.TotalTokens += other.TotalTokens
}

// Available reports whether a provider returned token usage. A successful
// request always consumes input tokens, so an all-zero value means the
// provider omitted usage metadata.
func (u TokenUsage) Available() bool {
	return u.InputTokens != 0 || u.OutputTokens != 0 || u.TotalTokens != 0
}

// ToolCallStatus is the status of a tool call.
type ToolCallStatus struct {
	Name      string
	Arguments []byte
	Output    string
	Err       error
}

// Message is a message in the session.
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
	// ThoughtSignature carries Google Gemini's thought_signature for
	// function calls when thinking is enabled. The API requires it to be
	// sent back verbatim in subsequent requests. Opaque to other providers;
	// json:"-" keeps it out of OpenAI/Anthropic-style serialization.
	ThoughtSignature string `json:"-"`
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
	MaxTokens      *int64
	ResponseFormat *string
	TrackUsage     bool
	ToolCaller     func(name string, data []byte) (string, error)
}

// Session is a session.
type Session []Message

func (cc Session) String() string {
	var sb strings.Builder
	for _, msg := range cc {
		content := TranscriptContent(msg)
		if content == "" {
			continue
		}
		switch msg.Role {
		case RoleSystem:
			sb.WriteString("**System**: ")
		case RoleUser:
			sb.WriteString("**User**: ")
		case RoleTool:
			sb.WriteString("**Tool**: ")
		case RoleAssistant:
			sb.WriteString("**Assistant**: ")
		}
		sb.WriteString(content)
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// TranscriptContent returns the human-readable transcript payload for a
// message. Unlike Content alone, it also includes provider tool-call metadata so
// saved sessions remain auditable and searchable.
func TranscriptContent(msg Message) string {
	var parts []string
	if msg.Role != RoleTool && strings.TrimSpace(msg.Content) != "" {
		parts = append(parts, msg.Content)
	}
	switch msg.Role {
	case RoleAssistant:
		for _, call := range msg.ToolCalls {
			if text := toolCallTranscript("Tool call", call, ""); text != "" {
				parts = append(parts, text)
			}
		}
	case RoleTool:
		for _, call := range msg.ToolCalls {
			if text := toolCallTranscript("Tool result", call, msg.Content); text != "" {
				parts = append(parts, text)
			}
		}
		if len(msg.ToolCalls) == 0 && msg.Content != "" {
			parts = append(parts, "Tool result:\n"+msg.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}

func toolCallTranscript(label string, call ToolCall, result string) string {
	name := call.Function.Name
	if name == "" {
		name = "tool"
	}
	var sb strings.Builder
	sb.WriteString(label)
	sb.WriteString(": ")
	sb.WriteString(name)
	if call.ID != "" {
		sb.WriteString(" (")
		sb.WriteString(call.ID)
		sb.WriteString(")")
	}
	if len(call.Function.Arguments) > 0 {
		sb.WriteString("\n")
		sb.WriteString(formatToolArguments(name, call.Function.Arguments))
	}
	if result != "" {
		status := "success"
		if call.IsError {
			status = "error"
		}
		sb.WriteString("\n")
		sb.WriteString("status: ")
		sb.WriteString(status)
		sb.WriteString("\n")
		sb.WriteString("output:\n")
		sb.WriteString(result)
	}
	return sb.String()
}

func formatToolArguments(name string, data []byte) string {
	if command := shellCommandArgument(name, data); command != "" {
		return "command:\n" + command
	}
	var v any
	if err := json.Unmarshal(data, &v); err == nil {
		if formatted, err := json.MarshalIndent(v, "", "  "); err == nil {
			return "arguments:\n" + string(formatted)
		}
	}
	return "arguments:\n" + string(data)
}

func shellCommandArgument(name string, data []byte) string {
	switch name {
	case "shell_run", "powershell_run":
	default:
		return ""
	}
	var args struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(data, &args); err != nil {
		return ""
	}
	return args.Command
}
