package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/selfhelp"
)

const UserInputToolName = "request_user_input"

const maxFormFields = 8

const (
	maxUserInputQuestionRunes    = 160
	maxUserInputLabelRunes       = 24
	maxUserInputPlaceholderRunes = 48
	maxUserInputOptionRunes      = 48
	maxUserInputOptionCount      = 8
)

const userInputToolDescription = "Pause and ask the local terminal user one necessary question or a short form of related fields. " +
	"Call this tool, not assistant text, when a tool workflow needs missing user input. " +
	"Keep it compact: the question is one short sentence of at most 80 characters with no preamble, field labels are 1-3 words, and examples or hints belong in the placeholder instead of the question or labels. " +
	"Use kind=text, select, multiselect, secret, or form. Use kind=multiselect when one or more choices may be selected. " +
	"Prefer select or multiselect with 2-8 concise options over free text whenever the choices are enumerable. " +
	"Never enumerate numbered options inside the question; pass them as an options array with kind=select. " +
	"Use kind=secret for passwords, tokens, cookies, or other credentials; never request a secret as ordinary text. " +
	"Use kind=form only when related fields belong together, such as a username plus password; otherwise ask the single most important field. " +
	"For shell credentials, request the secret with a target path under /secret_env/NAME for shell_run or powershell_run; after this tool returns a secret_ref, pass that opaque value unchanged in the downstream tool's secret_env map and reference the environment variable in the command. " +
	"Complete form example: {\"question\":\"OA login\",\"kind\":\"form\",\"fields\":[{\"key\":\"username\",\"label\":\"Username\",\"kind\":\"text\"},{\"key\":\"password\",\"label\":\"Password\",\"kind\":\"secret\",\"target\":{\"tool\":\"powershell_run\",\"path\":\"/secret_env/OA_PASSWORD\"}}]}. " +
	"If the response contains form.password.secret_ref=\"mods-secret://...\" and form.username.answer=\"alice\", call powershell_run with {\"command\":\"oa-cli --user alice\",\"secret_env\":{\"OA_PASSWORD\":\"mods-secret://...\"}} and read the password from $env:OA_PASSWORD."

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
	Answers   []string                 `json:"answers,omitempty"`
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
			Description: userInputToolDescription,
			InputSchema: objectSchema(map[string]any{
				"question": map[string]any{
					"type": "string", "maxLength": maxUserInputQuestionRunes,
					"description": "One short single-line question or prompt shown verbatim to the user; no preamble. Do not list options here; use kind=select with an options array.",
				},
				"kind": map[string]any{
					"type": "string", "enum": []string{"text", "select", "multiselect", "secret", "form"},
					"description": "text for free-form input, select for one choice, multiselect for one or more choices, secret for a masked credential, form for multiple related fields at once.",
				},
				"options": map[string]any{
					"type": "array", "minItems": 2, "maxItems": maxUserInputOptionCount,
					"items":       map[string]any{"type": "string", "maxLength": maxUserInputOptionRunes},
					"description": "Required for select and multiselect; 2 to 8 unique non-empty single-line choices.",
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
							"label": map[string]any{
								"type": "string", "maxLength": maxUserInputLabelRunes,
								"description": "Short single-line label of 1-3 words shown next to the field.",
							},
							"kind": map[string]any{
								"type":        "string",
								"enum":        []string{"text", "select", "secret"},
								"description": "Per-field input kind. Same rules as the top-level kind.",
							},
							"options": map[string]any{
								"type": "array", "minItems": 2, "maxItems": maxUserInputOptionCount,
								"items":       map[string]any{"type": "string", "maxLength": maxUserInputOptionRunes},
								"description": "Required when field kind=select; 2 to 8 unique non-empty single-line choices.",
							},
							"multiline": booleanProp("Allow Ctrl+J newlines when field kind=text."),
							"placeholder": map[string]any{
								"type": "string", "maxLength": maxUserInputPlaceholderRunes,
								"description": "Optional single-line hint shown when the field is empty; put examples here.",
							},
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
	if err := validateSingleLine("question", req.Question, maxUserInputQuestionRunes); err != nil {
		return err
	}
	if req.Kind != "select" && req.Kind != "multiselect" && questionEnumeratesOptions(req.Question) {
		return fmt.Errorf("question must not enumerate numbered options; pass them as options with kind=select (or a select form field) instead")
	}
	if req.Kind == "form" {
		return validateFormFields(req.Fields, req.Options, req.Multiline, req.Target)
	}
	if len(req.Fields) != 0 {
		return fmt.Errorf("%s input does not accept fields", req.Kind)
	}
	return validateInputKind(req.Kind, req.Options, req.Multiline, req.Target)
}

// validateSingleLine enforces the compact-display contract shared by the
// question, field labels, placeholders, and options: short, single-line text
// so the interaction panel never turns into a wall of wrapped prose.
func validateSingleLine(what, value string, maxRunes int) error {
	if strings.ContainsAny(value, "\n\r") {
		return fmt.Errorf("%s must be a single line", what)
	}
	if utf8.RuneCountInString(value) > maxRunes {
		return fmt.Errorf("%s must be at most %d characters; shorten it and move hints or examples to the placeholder", what, maxRunes)
	}
	return nil
}

var enumeratedOptionPatterns = []*regexp.Regexp{
	// "1. option", "1、选项", "1) option" — numbered marker followed by text
	// (a following digit is excluded so version strings like 1.22 stay inert).
	regexp.MustCompile(`(?:^|[\s（(，。？！、：;,.?!:])[1-9][0-9]?[.、)）](?:\s|[\p{Han}A-Za-z“"'（(])`),
	// "1 想启动…" — a bare number directly introducing CJK text.
	regexp.MustCompile(`(?:^|[\s（(，。？！、：;,.?!:])[1-9][0-9]?\s+[\p{Han}]`),
	// Circled enumerations ① through ⑳.
	regexp.MustCompile(`[①-⑳]`),
}

// questionEnumeratesOptions reports whether a question inlines a numbered
// option list ("1 foo 2 bar") that belongs in kind=select options instead.
// Two or more markers are required so ordinary numbers ("port 8080",
// "go 1.22") do not trip it.
func questionEnumeratesOptions(question string) bool {
	matches := 0
	for _, pattern := range enumeratedOptionPatterns {
		matches += len(pattern.FindAllString(question, -1))
	}
	return matches >= 2
}

// validateInputKind enforces the per-kind option/multiline/target rules shared
// by the top-level single-field request and each form field.
func validateInputKind(kind string, options []string, multiline bool, target UserInputTarget) error {
	switch kind {
	case "text":
		if len(options) != 0 || target.Tool != "" || target.Path != "" {
			return fmt.Errorf("text input does not accept options or target")
		}
	case "select", "multiselect":
		if multiline || target.Tool != "" || target.Path != "" {
			return fmt.Errorf("%s input does not accept multiline or target", kind)
		}
		if len(options) < 2 {
			return fmt.Errorf("%s input requires at least 2 options", kind)
		}
		if len(options) > maxUserInputOptionCount {
			return fmt.Errorf("%s input supports at most %d options", kind, maxUserInputOptionCount)
		}
		seen := map[string]bool{}
		for _, option := range options {
			if option == "" || seen[option] {
				return fmt.Errorf("%s options must be unique and non-empty", kind)
			}
			if err := validateSingleLine("option", option, maxUserInputOptionRunes); err != nil {
				return err
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
		if err := validateSingleLine(fmt.Sprintf("field %d (%s) label", i+1, field.Key), field.Label, maxUserInputLabelRunes); err != nil {
			return err
		}
		if field.Placeholder != "" {
			if err := validateSingleLine(fmt.Sprintf("field %d (%s) placeholder", i+1, field.Key), field.Placeholder, maxUserInputPlaceholderRunes); err != nil {
				return err
			}
		}
		if field.Kind == "multiselect" {
			return fmt.Errorf("field %d (%s): form fields do not support multiselect", i+1, field.Key)
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
