package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/proto"
)

// RegisterThinking registers a lightweight sequential thinking note tool.
func RegisterThinking(registry *Registry) error {
	return registry.Register(Tool{
		Kind:            ToolKindBuiltin,
		Capabilities:    ToolCapabilities{ReadOnly: true},
		IntentExtractor: func(json.RawMessage) approval.AccessIntent { return approval.AccessIntent{Class: approval.AccessRead} },
		Spec: proto.ToolSpec{
			Name:        "thinking_note",
			Description: "Record one concise reasoning step, next step, and whether the task is complete.",
			InputSchema: objectSchema(map[string]any{
				"thought":   stringProp("Current concise reasoning step."),
				"next_step": stringProp("The next action to take."),
				"done": map[string]any{
					"type":        "boolean",
					"description": "Whether the reasoning process is complete.",
				},
			}, "thought"),
		},
		Call: func(_ context.Context, data json.RawMessage) (string, error) {
			var args struct {
				Thought  string `json:"thought"`
				NextStep string `json:"next_step"`
				Done     bool   `json:"done"`
			}
			if err := decodeArgs(data, &args); err != nil {
				return "", err
			}
			return fmt.Sprintf("thought: %s\nnext_step: %s\ndone: %v", args.Thought, args.NextStep, args.Done), nil
		},
	})
}
