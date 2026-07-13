// Package ollamaapi contains the small, provider-facing subset of Ollama's
// public REST schema used by mods. Keeping this transport local avoids pulling
// Ollama's command TUI (and its Charm v1 dependencies) into the module graph.
package ollamaapi

import (
	"encoding/json"
	"fmt"
	"time"
)

type StatusError struct {
	StatusCode   int
	Status       string
	ErrorMessage string `json:"error"`
}

func (e StatusError) Error() string {
	switch {
	case e.Status != "" && e.ErrorMessage != "":
		return fmt.Sprintf("%s: %s", e.Status, e.ErrorMessage)
	case e.Status != "":
		return e.Status
	case e.ErrorMessage != "":
		return e.ErrorMessage
	default:
		return "something went wrong, please see the ollama server logs for details"
	}
}

type AuthorizationError struct {
	StatusCode int
	Status     string
	SigninURL  string `json:"signin_url"`
}

func (e AuthorizationError) Error() string {
	if e.Status != "" {
		return e.Status
	}
	return "something went wrong, please see the ollama server logs for details"
}

type ImageData []byte

type ChatRequest struct {
	Model    string         `json:"model"`
	Messages []Message      `json:"messages"`
	Stream   *bool          `json:"stream,omitempty"`
	Tools    []Tool         `json:"tools,omitempty"`
	Options  map[string]any `json:"options"`
}

type Message struct {
	Role      string      `json:"role"`
	Content   string      `json:"content"`
	Images    []ImageData `json:"images,omitempty"`
	ToolCalls []ToolCall  `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Index     int                       `json:"index"`
	Name      string                    `json:"name"`
	Arguments ToolCallFunctionArguments `json:"arguments"`
}

type ToolCallFunctionArguments map[string]any

func (a ToolCallFunctionArguments) String() string {
	if a == nil {
		return "{}"
	}
	data, _ := json.Marshal(a)
	return string(data)
}

type Tool struct {
	Type     string       `json:"type"`
	Items    any          `json:"items,omitempty"`
	Function ToolFunction `json:"function"`
}

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type ChatResponse struct {
	Model     string    `json:"model"`
	CreatedAt time.Time `json:"created_at"`
	Message   Message   `json:"message"`
	Done      bool      `json:"done"`
	Metrics
}

type Metrics struct {
	PromptEvalCount int `json:"prompt_eval_count,omitempty"`
	EvalCount       int `json:"eval_count,omitempty"`
}
