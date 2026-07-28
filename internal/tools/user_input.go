package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"unicode"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/selfhelp"
)

const UserInputToolName = "request_user_input"

const maxFormFields = 8

type UserInputTarget struct {
	Tool string `json:"tool"`
	Path string `json:"path"`
}

type UserInputRequest struct {
	Question  string           `json:"question"`
	Kind      string           `json:"kind"`
	Options   []string         `json:"options,omitempty"`
	Multiline bool             `json:"multiline,omitempty"`
	Target    UserInputTarget  `json:"target,omitempty"`
	Fields    []UserInputField `json:"fields,omitempty"`
}

// UserInputField describes one field of a kind=form request.
type UserInputField struct {
	Key         string          `json:"key"`
	Label       string          `json:"label"`
	Kind        string          `json:"kind"`
	Options     []string        `json:"options,omitempty"`
	Multiline   bool            `json:"multiline,omitempty"`
	Target      UserInputTarget `json:"target,omitempty"`
	Placeholder string          `json:"placeholder,omitempty"`
}

type UserInputResponse struct {
	Answer    string                   `json:"answer,omitempty"`
	SecretRef string                   `json:"secret_ref,omitempty"`
	Form      map[string]FieldResponse `json:"form,omitempty"`
}

// FieldResponse mirrors UserInputResponse for one field of a kind=form result.
type FieldResponse struct {
	Answer    string `json:"answer,omitempty"`
	SecretRef string `json:"secret_ref,omitempty"`
}

type UserInputHandler func(context.Context, UserInputRequest) (UserInputResponse, error)

type SecretPromptHandler func(context.Context, string, string) (string, error)

type InteractionHandlers struct {
	UserInput     UserInputHandler
	SudoPrompt    SecretPromptHandler
	ShellProgress ShellProgressHandler
	SelfHelp      selfhelp.Reference
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
			Description: "Pause and ask the local terminal user one necessary question or a short form of related fields. Use kind=secret for passwords, tokens, cookies, or other credentials; never request a secret as ordinary text. Use kind=form when two or more fields belong together (e.g. username and password).",
			InputSchema: objectSchema(map[string]any{
				"question": stringProp("A concise question or prompt shown verbatim to the user."),
				"kind": map[string]any{
					"type": "string", "enum": []string{"text", "select", "secret", "form"},
					"description": "text for free-form input, select for one choice, secret for a masked credential, form for multiple related fields at once.",
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
				"fields": map[string]any{
					"type":        "array",
					"description": "Required for form. 1 to 8 fields collected together in one dialog.",
					"minItems":    1,
					"maxItems":    maxFormFields,
					"items": map[string]any{
						"type":     "object",
						"required": []string{"key", "label", "kind"},
						"properties": map[string]any{
							"key": map[string]any{
								"type":        "string",
								"pattern":     "^[a-zA-Z_][a-zA-Z0-9_]*$",
								"description": "Unique key within the form. Returned as the response map key.",
							},
							"label":       stringProp("Short label shown to the user."),
							"kind": map[string]any{
								"type":        "string",
								"enum":        []string{"text", "select", "secret"},
								"description": "Per-field input kind. Same rules as the top-level kind.",
							},
							"options": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Required when field kind=select; 2 to 5 unique non-empty choices.",
							},
							"multiline":   booleanProp("Allow Ctrl+J newlines when field kind=text."),
							"placeholder": stringProp("Optional placeholder shown when the field is empty."),
							"target": map[string]any{
								"type":        "object",
								"description": "Required when field kind=secret. Binds this secret to one downstream tool argument.",
								"properties": map[string]any{
									"tool": stringProp("Exact downstream MCP or shell tool name."),
									"path": stringProp("RFC 6901 JSON Pointer to the downstream argument."),
								},
								"required": []string{"tool", "path"},
							},
						},
					},
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
	if req.Kind == "form" {
		return validateFormFields(req.Fields, req.Options, req.Multiline, req.Target)
	}
	if len(req.Fields) != 0 {
		return fmt.Errorf("%s input does not accept fields", req.Kind)
	}
	return validateInputKind(req.Kind, req.Options, req.Multiline, req.Target)
}

// validateInputKind enforces the per-kind option/multiline/target rules shared
// by the top-level single-field request and each form field.
func validateInputKind(kind string, options []string, multiline bool, target UserInputTarget) error {
	switch kind {
	case "text":
		if len(options) != 0 || target.Tool != "" || target.Path != "" {
			return fmt.Errorf("text input does not accept options or target")
		}
	case "select":
		if multiline || target.Tool != "" || target.Path != "" {
			return fmt.Errorf("select input does not accept multiline or target")
		}
		if len(options) < 2 || len(options) > 5 {
			return fmt.Errorf("select input requires 2 to 5 options")
		}
		seen := map[string]bool{}
		for _, option := range options {
			if option == "" || seen[option] {
				return fmt.Errorf("select options must be unique and non-empty")
			}
			seen[option] = true
		}
	case "secret":
		if multiline || len(options) != 0 {
			return fmt.Errorf("secret input does not accept multiline or options")
		}
		if target.Tool == "" || target.Path == "" || target.Path[0] != '/' {
			return fmt.Errorf("secret input requires a target tool and RFC 6901 path")
		}
	default:
		return fmt.Errorf("unsupported input kind %q", kind)
	}
	return nil
}

func validateFormFields(fields []UserInputField, options []string, multiline bool, target UserInputTarget) error {
	if len(options) != 0 || target.Tool != "" || target.Path != "" || multiline {
		return fmt.Errorf("form input does not accept options, target, or multiline")
	}
	if len(fields) == 0 {
		return fmt.Errorf("form input requires at least one field")
	}
	if len(fields) > maxFormFields {
		return fmt.Errorf("form input supports at most %d fields", maxFormFields)
	}
	seenKeys := map[string]bool{}
	seenPaths := map[string]bool{}
	for i, field := range fields {
		if !validFieldKey(field.Key) {
			return fmt.Errorf("field %d: key must match ^[a-zA-Z_][a-zA-Z0-9_]*$", i+1)
		}
		if seenKeys[field.Key] {
			return fmt.Errorf("field %d: duplicate key %q", i+1, field.Key)
		}
		seenKeys[field.Key] = true
		if field.Label == "" {
			return fmt.Errorf("field %d (%s): label is required", i+1, field.Key)
		}
		if err := validateInputKind(field.Kind, field.Options, field.Multiline, field.Target); err != nil {
			return fmt.Errorf("field %d (%s): %w", i+1, field.Key, err)
		}
		if field.Kind == "secret" {
			if seenPaths[field.Target.Path] {
				return fmt.Errorf("field %d (%s): duplicate secret target path %q", i+1, field.Key, field.Target.Path)
			}
			seenPaths[field.Target.Path] = true
		}
	}
	return nil
}

func validFieldKey(key string) bool {
	if key == "" {
		return false
	}
	for i, r := range key {
		if i == 0 && !(unicode.IsLetter(r) || r == '_') {
			return false
		}
		if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_') {
			return false
		}
	}
	return true
}
