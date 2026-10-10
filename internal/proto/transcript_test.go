package proto

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestTranscriptPreservesToolArguments(t *testing.T) {
	for _, name := range []string{"shell_run", "powershell_run", "lookup"} {
		t.Run(name, func(t *testing.T) {
			args := []byte(`{"command":"echo first\necho second","cwd":"/tmp/example","timeout_ms":1500,"id":9007199254740993,"secret_env":{"TOKEN":"secret:ref"}}`)
			for _, role := range []string{RoleAssistant, RoleTool} {
				content := TranscriptContent(Message{Role: role, ToolCalls: []ToolCall{{Function: Function{Name: name, Arguments: args}}}})
				for _, expected := range []string{"/tmp/example", "1500", "9007199254740993", "secret:ref"} {
					require.Contains(t, content, expected)
				}
				if name != "lookup" {
					require.Contains(t, content, "command:\necho first\necho second")
				}
			}
		})
	}
	for _, args := range []string{`{"value":1.23000000000000000001}`, `{"value":1e1000}`, `{"incomplete":`, "literal custom payload"} {
		t.Run(args, func(t *testing.T) {
			content := formatToolArguments("custom", []byte(args))
			if args == `{"value":1.23000000000000000001}` {
				require.Contains(t, content, "1.23000000000000000001")
			} else if args == `{"value":1e1000}` {
				require.Contains(t, content, "1e1000")
			} else {
				require.Equal(t, "arguments:\n"+args, content)
			}
		})
	}
}

func TestTranscriptEmptyToolResultHasStatus(t *testing.T) {
	for _, failed := range []bool{false, true} {
		call := ToolCall{ID: "call-1", IsError: failed, Function: Function{Name: "shell_run"}}
		text := TranscriptContent(Message{Role: RoleTool, ToolCalls: []ToolCall{call}})
		status := "success"
		if failed {
			status = "error"
		}
		require.Contains(t, text, "status: "+status)
		require.Contains(t, text, "output:\n(no output)")
		require.NotContains(t, TranscriptContent(Message{Role: RoleAssistant, ToolCalls: []ToolCall{call}}), "status:")
	}
}
