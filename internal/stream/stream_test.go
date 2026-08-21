package stream

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/proto"
)

func callToolForTest(id, name string, data []byte, caller func(string, []byte) (string, error)) (proto.Message, proto.ToolCallStatus) {
	return CallTool(proto.ToolCallRequest{ID: id, Index: 1, Total: 1, Name: name, Arguments: data}, func(call proto.ToolCallRequest) (string, error) {
		return caller(call.Name, call.Arguments)
	})
}

func TestCallToolPreservesResults(t *testing.T) {
	t.Run("short result unchanged", func(t *testing.T) {
		short := "hello world"
		msg, status := callToolForTest("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return short, nil
		})
		if msg.Content != short {
			t.Errorf("expected %q, got %q", short, msg.Content)
		}
		if status.Err != nil {
			t.Errorf("unexpected error: %v", status.Err)
		}
	})

	t.Run("large result unchanged", func(t *testing.T) {
		long := strings.Repeat("x", 100000)
		msg, status := callToolForTest("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return long, nil
		})
		if msg.Content != long {
			t.Errorf("expected complete tool result")
		}
		if status.Err != nil {
			t.Errorf("unexpected error: %v", status.Err)
		}
	})

	t.Run("large unicode result unchanged", func(t *testing.T) {
		long := strings.Repeat("你", 40000)
		msg, _ := callToolForTest("tid", "tool", nil, func(string, []byte) (string, error) {
			return long, nil
		})
		if msg.Content != long {
			t.Fatal("expected complete UTF-8 tool result")
		}
	})

	t.Run("error result has error in content", func(t *testing.T) {
		msg, status := callToolForTest("tid", "tool", nil, func(name string, data []byte) (string, error) {
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
		msg, status := callToolForTest("tid", "tool", nil, func(name string, data []byte) (string, error) {
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
		_, status := callToolForTest("tid", "tool", args, func(name string, data []byte) (string, error) {
			// Take measurable time: on Windows the monotonic clock is too
			// coarse for an instant call to register a positive duration.
			time.Sleep(2 * time.Millisecond)
			return "result", nil
		})
		if string(status.Arguments) != string(args) {
			t.Errorf("expected args %q, got %q", args, status.Arguments)
		}
		if status.ID != "tid" || status.Index != 1 || status.Total != 1 {
			t.Errorf("expected call metadata to be preserved, got %#v", status)
		}
		if status.Duration <= 0 {
			t.Error("expected call duration to be recorded")
		}
	})

	t.Run("large error result is unchanged", func(t *testing.T) {
		longErr := strings.Repeat("e", 100000)
		msg, _ := callToolForTest("tid", "tool", nil, func(name string, data []byte) (string, error) {
			return longErr, errors.New("failed")
		})
		if msg.Content != longErr {
			t.Error("expected complete error result")
		}
	})
}
