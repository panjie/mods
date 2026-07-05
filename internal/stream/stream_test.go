package stream

import (
	"errors"
	"strings"
	"testing"
)

func TestCallTool_truncatesLongResults(t *testing.T) {
	t.Run("short result unchanged", func(t *testing.T) {
		short := "hello world"
		msg, status := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return short, nil
		})
		if msg.Content != short {
			t.Errorf("expected %q, got %q", short, msg.Content)
		}
		if status.Err != nil {
			t.Errorf("unexpected error: %v", status.Err)
		}
	})

	t.Run("exactly maxToolResultChars unchanged", func(t *testing.T) {
		long := strings.Repeat("x", maxToolResultChars)
		msg, status := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return long, nil
		})
		if msg.Content != long {
			t.Errorf("expected exactly maxToolResultChars chars unchanged")
		}
		if status.Err != nil {
			t.Errorf("unexpected error: %v", status.Err)
		}
	})

	t.Run("over maxToolResultChars truncated with hint", func(t *testing.T) {
		long := strings.Repeat("x", maxToolResultChars+1000)
		msg, status := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return long, nil
		})
		if len(msg.Content) == len(long) {
			t.Errorf("expected truncation, got same length")
		}
		if !strings.Contains(msg.Content, "truncated at") {
			t.Errorf("expected truncation hint in: %q", msg.Content)
		}
		if !strings.Contains(msg.Content, "Use more specific tools") {
			t.Errorf("expected guidance hint in: %q", msg.Content)
		}
		if status.Err != nil {
			t.Errorf("unexpected error: %v", status.Err)
		}
	})

	t.Run("error result has error in content", func(t *testing.T) {
		msg, status := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return "", errors.New("test failure")
		})
		if msg.Content != "test failure" {
			t.Errorf("expected error message in content, got %q", msg.Content)
		}
		if status.Err == nil {
			t.Error("expected status to have error")
		}
	})

	t.Run("error result preserves returned content", func(t *testing.T) {
		msg, status := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return "stdout\nstderr\n[exit status 7]", errors.New("command exited with status 7")
		})
		if msg.Content != "stdout\nstderr\n[exit status 7]" {
			t.Errorf("expected returned content to be preserved, got %q", msg.Content)
		}
		if len(msg.ToolCalls) != 1 || !msg.ToolCalls[0].IsError {
			t.Errorf("expected tool call to be marked as error: %#v", msg.ToolCalls)
		}
		if status.Err == nil {
			t.Error("expected status to have error")
		}
		if status.Output != msg.Content {
			t.Errorf("expected status.Output to mirror content, got %q", status.Output)
		}
	})

	t.Run("arguments are stored in status", func(t *testing.T) {
		args := []byte(`{"query":"test"}`)
		_, status := CallTool("tid", "tool", args, func(name string, data []byte) (string, error) {
			return "result", nil
		})
		if string(status.Arguments) != string(args) {
			t.Errorf("expected args %q, got %q", args, status.Arguments)
		}
	})

	t.Run("long error result is also truncated", func(t *testing.T) {
		longErr := strings.Repeat("e", maxToolResultChars+500)
		msg, _ := CallTool("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return longErr, nil // content = error text
		})
		if len(msg.Content) >= len(longErr) {
			t.Errorf("expected truncation of long error content")
		}
	})
}
