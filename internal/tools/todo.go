package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/panjie/mods/internal/proto"
)

const TodoWriteToolName = "todo_write"

const (
	todoStatusPending    = "pending"
	todoStatusInProgress = "in_progress"
	todoStatusCompleted  = "completed"
)

const maxTodoItems = 20

func RegisterTodoWrite(registry *Registry) error {
	return registry.Register(Tool{
		Kind:         ToolKindBuiltin,
		Capabilities: ToolCapabilities{ReadOnly: true},
		Spec: proto.ToolSpec{
			Name:        TodoWriteToolName,
			Description: "Create or update the session task plan so the user can follow progress. Send the FULL list of steps on every call; each call replaces the previous plan. Write the plan before starting work that needs several steps, then update statuses as execution progresses.",
			InputSchema: objectSchema(map[string]any{
				"todos": map[string]any{
					"type":        "array",
					"description": "Complete list of plan steps; resend the whole list on every update.",
					"minItems":    1,
					"maxItems":    maxTodoItems,
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"content": stringProp("Short imperative step description."),
							"status": map[string]any{
								"type":        "string",
								"enum":        []string{todoStatusPending, todoStatusInProgress, todoStatusCompleted},
								"description": "Step status: pending, in_progress, or completed.",
							},
						},
						"required": []string{"content", "status"},
					},
				},
			}, "todos"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Todos []todoItem `json:"todos"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return formatTodoItems(args.Todos)
		},
	})
}

type todoItem struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

func formatTodoItems(items []todoItem) (string, error) {
	if len(items) == 0 {
		return "", fmt.Errorf("todos must contain at least 1 item")
	}
	if len(items) > maxTodoItems {
		return "", fmt.Errorf("todos must contain at most %d items", maxTodoItems)
	}
	completed := 0
	inProgress := 0
	for i := range items {
		content := strings.TrimSpace(items[i].Content)
		if content == "" {
			return "", fmt.Errorf("item %d: content is required", i+1)
		}
		switch items[i].Status {
		case todoStatusCompleted:
			completed++
		case todoStatusInProgress:
			inProgress++
		case todoStatusPending:
		default:
			return "", fmt.Errorf("item %d: status must be pending, in_progress, or completed", i+1)
		}
		items[i].Content = content
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Plan (%d items)", len(items))
	var parts []string
	if completed > 0 {
		parts = append(parts, fmt.Sprintf("%d completed", completed))
	}
	if inProgress > 0 {
		parts = append(parts, fmt.Sprintf("%d in progress", inProgress))
	}
	if len(parts) > 0 {
		b.WriteString(" — ")
		b.WriteString(strings.Join(parts, ", "))
	}
	for i, item := range items {
		marker := "[ ]"
		switch item.Status {
		case todoStatusCompleted:
			marker = "[x]"
		case todoStatusInProgress:
			marker = "[~]"
		}
		fmt.Fprintf(&b, "\n%d. %s %s", i+1, marker, item.Content)
	}
	return b.String(), nil
}
