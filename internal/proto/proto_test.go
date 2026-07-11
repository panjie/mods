package proto

import (
	"testing"

	"github.com/charmbracelet/x/exp/golden"
)

func TestTokenUsageAddAndAvailable(t *testing.T) {
	var usage TokenUsage
	if usage.Available() {
		t.Fatal("zero usage must be unavailable")
	}
	usage.Add(TokenUsage{InputTokens: 10, OutputTokens: 4, TotalTokens: 14})
	usage.Add(TokenUsage{InputTokens: 8, OutputTokens: 2, TotalTokens: 10})
	if usage != (TokenUsage{InputTokens: 18, OutputTokens: 6, TotalTokens: 24}) {
		t.Fatalf("unexpected accumulated usage: %#v", usage)
	}
	if !usage.Available() {
		t.Fatal("non-zero usage must be available")
	}
}

func TestStringer(t *testing.T) {
	messages := []Message{
		{
			Role:    RoleSystem,
			Content: "you are a medieval king",
		},
		{
			Role:    RoleUser,
			Content: "first 4 natural numbers",
		},
		{
			Role:    RoleAssistant,
			Content: "1, 2, 3, 4",
		},
		{
			Role:    RoleTool,
			Content: `{"the":"result"}`,
			ToolCalls: []ToolCall{
				{
					ID: "aaa",
					Function: Function{
						Name:      "myfunc",
						Arguments: []byte(`{"a":"b"}`),
					},
				},
			},
		},
		{
			Role:    RoleUser,
			Content: "as a json array",
		},
		{
			Role:    RoleAssistant,
			Content: "[ 1, 2, 3, 4 ]",
		},
		{
			Role:    RoleAssistant,
			Content: "something from an assistant",
		},
	}

	golden.RequireEqual(t, []byte(Session(messages).String()))
}
