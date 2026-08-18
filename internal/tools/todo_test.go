package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTodoWrite(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterTodoWrite(reg))

	tool, ok := reg.Tool(TodoWriteToolName)
	require.True(t, ok)
	require.True(t, tool.Capabilities.ReadOnly)
	require.False(t, tool.Capabilities.Mutable)
	require.False(t, tool.Capabilities.ShellExecution)
	required, hasRequired := tool.Spec.InputSchema["required"].([]string)
	require.True(t, hasRequired)
	require.Equal(t, []string{"todos"}, required)

	got, err := reg.Call(context.Background(), TodoWriteToolName, []byte(`{"todos":[
		{"content":"measure current startup time","status":"completed"},
		{"content":"analyze init.el load cost","status":"in_progress"},
		{"content":"apply lazy-loading and re-measure","status":"pending"}
	]}`))
	require.NoError(t, err)
	require.Contains(t, got, "Plan (3 items)")
	require.Contains(t, got, "1 completed, 1 in progress")
	require.Contains(t, got, "1. [x] measure current startup time")
	require.Contains(t, got, "2. [~] analyze init.el load cost")
	require.Contains(t, got, "3. [ ] apply lazy-loading and re-measure")

	// Whole-list replacement semantics: a second call with fewer items
	// reports only those items — the tool keeps no state between calls.
	got, err = reg.Call(context.Background(), TodoWriteToolName, []byte(`{"todos":[
		{"content":"only step","status":"pending"}
	]}`))
	require.NoError(t, err)
	require.Contains(t, got, "Plan (1 items)")
	require.NotContains(t, got, "measure current startup time")
}

func TestTodoWriteValidation(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, RegisterTodoWrite(reg))

	calls := func(payload string) error {
		_, err := reg.Call(context.Background(), TodoWriteToolName, []byte(payload))
		return err
	}

	t.Run("empty list", func(t *testing.T) {
		require.ErrorContains(t, calls(`{"todos":[]}`), "at least 1")
	})

	t.Run("missing todos", func(t *testing.T) {
		require.ErrorContains(t, calls(`{}`), "at least 1")
	})

	t.Run("too many items", func(t *testing.T) {
		payload := `{"todos":[`
		for i := 0; i < 21; i++ {
			if i > 0 {
				payload += ","
			}
			payload += `{"content":"s","status":"pending"}`
		}
		payload += `]}`
		require.ErrorContains(t, calls(payload), "at most 20")
	})

	t.Run("invalid status", func(t *testing.T) {
		require.ErrorContains(t,
			calls(`{"todos":[{"content":"a","status":"done"}]}`),
			"item 1: status must be")
	})

	t.Run("empty content", func(t *testing.T) {
		require.ErrorContains(t,
			calls(`{"todos":[{"content":"   ","status":"pending"}]}`),
			"item 1: content is required")
	})

	t.Run("invalid json", func(t *testing.T) {
		require.Error(t, calls(`{nope`))
	})
}
