package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/panjie/mods/internal/proto"
)

const UserInputToolName = "request_user_input"

type UserInputTarget struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
}

type UserInputRequest struct {
	Question  string          `json:"question"`
	Kind      string          `json:"kind"`
	Options   []string        `json:"options,omitempty"`
	Multiline bool            `json:"multiline,omitempty"`
	Target    UserInputTarget `json:"target,omitempty"`
}

type UserInputResponse struct {
	Answer    string `json:"answer,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
}

type UserInputHandler func(context.Context, UserInputRequest) (UserInputResponse, error)

type SecretPromptHandler func(context.Context, string, string) (string, error)

type InteractionHandlers struct {
	UserInput  UserInputHandler
	SudoPrompt SecretPromptHandler
}

// RegisterUserInput registers the provider-visible request_user_input tool.
// The application supplies handler because terminal interaction belongs to
// Bubble Tea rather than the provider-neutral registry package.
func RegisterUserInput(registry *Registry, handler UserInputHandler) error {
	if handler == nil {
		handler = func(context.Context, UserInputRequest) (UserInputResponse, error) {
			return UserInputResponse{}, fmt.Errorf("interactive user input is unavailable")
		}
	}
	return registry.Register(Tool{
		Kind:          ToolKindBuiltin,
		TimeoutPolicy: TimeoutPolicySelf,
		Capabilities: ToolCapabilities{
			ReadOnly:    true,
			Interactive: true,
		},
		Spec: proto.ToolSpec{
			Name:        UserInputToolName,
			Description: "Pause and ask the local terminal user one necessary question. Use kind=secret for passwords, tokens, cookies, or other credentials; never request a secret as ordinary text.",
			InputSchema: objectSchema(map[string]any{
				"question": stringProp("A concise question shown verbatim to the user."),
				"kind": map[string]any{
					"type": "string", "enum": []string{"text", "select", "secret"},
					"description": "text for free-form input, select for one choice, secret for a masked credential.",
				},
				"options": map[string]any{
					"type": "array", "items": map[string]any{"type": "string"},
					"description": "Required for select; 2 to 5 unique non-empty choices.",
				},
				"multiline": booleanProp("Allow Ctrl+J newlines for text input."),
				"target": map[string]any{
					"type":        "object",
					"description": "Required for secret. Binds the secret to one future tool argument.",
					"properties": map[string]any{
						"tool": stringProp("Exact downstream MCP or shell tool name."),
						"path": stringProp("RFC 6901 JSON Pointer to the downstream argument."),
					},
					"required": []string{"tool", "path"},
				},
			}, "question", "kind"),
		},
		Call: func(ctx context.Context, data json.RawMessage) (string, error) {
			var req UserInputRequest
			if err := decodeArgs(data, &req); err != nil {
				return "", err
			}
			if err := validateUserInputRequest(req); err != nil {
				return "", err
			}
			resp, err := handler(ctx, req)
			if err != nil {
				return "", err
			}
			out, err := json.Marshal(resp)
			if err != nil {
				return "", fmt.Errorf("encode user input response: %w", err)
			}
			return string(out), nil
		},
	})
}

func validateUserInputRequest(req UserInputRequest) error {
	if req.Question == "" {
		return fmt.Errorf("question is required")
	}
	switch req.Kind {
	case "text":
		if len(req.Options) != 0 || req.Target.Tool != "" || req.Target.Path != "" {
			return fmt.Errorf("text input does not accept options or target")
		}
	case "select":
		if req.Multiline || req.Target.Tool != "" || req.Target.Path != "" {
			return fmt.Errorf("select input does not accept multiline or target")
		}
		if len(req.Options) < 2 || len(req.Options) > 5 {
			return fmt.Errorf("select input requires 2 to 5 options")
		}
		seen := map[string]bool{}
		for _, option := range req.Options {
			if option == "" || seen[option] {
				return fmt.Errorf("select options must be unique and non-empty")
			}
			seen[option] = true
		}
	case "secret":
		if req.Multiline || len(req.Options) != 0 {
			return fmt.Errorf("secret input does not accept multiline or options")
		}
		if req.Target.Tool == "" || req.Target.Path == "" || req.Target.Path[0] != '/' {
			return fmt.Errorf("secret input requires a target tool and RFC 6901 path")
		}
	default:
		return fmt.Errorf("unsupported input kind %q", req.Kind)
	}
	return nil
}
