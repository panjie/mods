package anthropic

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/panjie/mods/internal/proto"
)

const messagesProviderDataKey = "anthropic.messages.content"

func fromToolSpecs(specs []proto.ToolSpec) []anthropic.ToolUnionParam {
	var tools []anthropic.ToolUnionParam
	for _, spec := range specs {
		props := map[string]any{}
		if schemaProps, ok := spec.InputSchema["properties"].(map[string]any); ok {
			props = proto.StripSchema(schemaProps)
		}
		required := schemaRequired(spec.InputSchema)
		tools = append(tools, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				InputSchema: anthropic.ToolInputSchemaParam{
					Properties: props,
					Required:   required,
				},
				Name:        spec.Name,
				Description: anthropic.String(spec.Description),
			},
		})
	}
	return tools
}

func schemaRequired(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	switch required := schema["required"].(type) {
	case []string:
		return append([]string(nil), required...)
	case []any:
		out := make([]string, 0, len(required))
		for _, item := range required {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func fromProtoMessages(input []proto.Message) (
	system []anthropic.TextBlockParam,
	messages []anthropic.MessageParam,
	err error,
) {
	input = proto.NormalizeSystemMessages(input)
	for i := 0; i < len(input); i++ {
		msg := input[i]
		switch msg.Role {
		case proto.RoleSystem:
			// system is not a role in anthropic, must set it as the system part of the request.
			system = append(system, *anthropic.NewTextBlock(msg.Content).OfText)
		case proto.RoleTool:
			// Tool results from one parallel assistant call must be returned in
			// one user message, with one block per tool_use ID.
			var blocks []anthropic.ContentBlockParamUnion
			for i < len(input) && input[i].Role == proto.RoleTool {
				toolMsg := input[i]
				for _, call := range toolMsg.ToolCalls {
					blocks = append(blocks, newToolResultBlock(call.ID, toolMsg.Content, call.IsError))
				}
				i++
			}
			i--
			if len(blocks) > 0 {
				messages = append(messages, anthropic.NewUserMessage(blocks...))
			}
		case proto.RoleUser:
			if len(msg.Images) > 0 {
				var blocks []anthropic.ContentBlockParamUnion
				for _, img := range msg.Images {
					b64 := base64.StdEncoding.EncodeToString(img.Data)
					blocks = append(blocks, anthropic.NewImageBlockBase64(img.MimeType, b64))
				}
				if msg.Content != "" {
					blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
				}
				messages = append(messages, anthropic.NewUserMessage(blocks...))
			} else {
				block := anthropic.NewTextBlock(msg.Content)
				messages = append(messages, anthropic.NewUserMessage(block))
			}
		case proto.RoleAssistant:
			if state := msg.ProviderData[messagesProviderDataKey]; len(state) > 0 {
				blocks, decodeErr := decodeProviderContent(state)
				if decodeErr != nil {
					return nil, nil, decodeErr
				}
				messages = append(messages, anthropic.NewAssistantMessage(blocks...))
				continue
			}
			var blocks []anthropic.ContentBlockParamUnion
			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}
			for _, tool := range msg.ToolCalls {
				block := anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tool.ID,
						Name:  tool.Function.Name,
						Input: json.RawMessage(tool.Function.Arguments),
					},
				}
				blocks = append(blocks, block)
			}
			if len(blocks) > 0 {
				messages = append(messages, anthropic.NewAssistantMessage(blocks...))
			}
		}
	}
	return system, messages, nil
}

func decodeProviderContent(state json.RawMessage) ([]anthropic.ContentBlockParamUnion, error) {
	var rawBlocks []json.RawMessage
	if err := json.Unmarshal(state, &rawBlocks); err != nil {
		return nil, fmt.Errorf("decode Anthropic provider content: %w", err)
	}
	blocks := make([]anthropic.ContentBlockParamUnion, len(rawBlocks))
	for i, raw := range rawBlocks {
		if err := json.Unmarshal(raw, &blocks[i]); err != nil {
			return nil, fmt.Errorf("decode Anthropic provider content block %d: %w", i, err)
		}
	}
	return blocks, nil
}

func toProtoMessage(in anthropic.Message) (proto.Message, error) {
	msg := proto.Message{
		Role: string(in.Role),
	}
	rawBlocks := make([]json.RawMessage, 0, len(in.Content))

	for _, block := range in.Content {
		raw := block.RawJSON()
		if raw == "" {
			encoded, err := json.Marshal(block.ToParam())
			if err != nil {
				return proto.Message{}, fmt.Errorf("encode Anthropic response content block: %w", err)
			}
			raw = string(encoded)
		}
		rawBlocks = append(rawBlocks, json.RawMessage(raw))

		switch value := block.AsAny().(type) {
		case anthropic.TextBlock:
			msg.Content += value.Text
		case anthropic.ToolUseBlock:
			msg.ToolCalls = append(msg.ToolCalls, proto.ToolCall{
				ID: value.ID,
				Function: proto.Function{
					Name:      value.Name,
					Arguments: append([]byte(nil), value.Input...),
				},
			})
		}
	}

	if len(rawBlocks) > 0 {
		state, err := json.Marshal(rawBlocks)
		if err != nil {
			return proto.Message{}, fmt.Errorf("encode Anthropic provider content: %w", err)
		}
		msg.ProviderData = map[string]json.RawMessage{
			messagesProviderDataKey: state,
		}
	}
	return msg, nil
}

// anthropic v1.5 removed this method, copied it back here so we don't need to
// refactor too much.
func newToolResultBlock(toolUseID string, content string, isError bool) anthropic.ContentBlockParamUnion {
	toolBlock := anthropic.ToolResultBlockParam{
		ToolUseID: toolUseID,
		Content: []anthropic.ToolResultBlockParamContentUnion{
			{OfText: &anthropic.TextBlockParam{Text: content}},
		},
		IsError: anthropic.Bool(isError),
	}
	return anthropic.ContentBlockParamUnion{OfToolResult: &toolBlock}
}
