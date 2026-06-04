package openai

import (
	"encoding/base64"
	"fmt"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared/constant"
)

func fromToolSpecs(specs []proto.ToolSpec) []openai.ChatCompletionToolParam {
	var tools []openai.ChatCompletionToolParam
	for _, spec := range specs {
		params := stripSchema(spec.InputSchema)
		if params == nil {
			params = map[string]any{"type": "object"}
		}

		tools = append(tools, openai.ChatCompletionToolParam{
			Type: constant.Function("function"),
			Function: openai.FunctionDefinitionParam{
				Name:        spec.Name,
				Description: openai.String(spec.Description),
				Parameters:  params,
			},
		})
	}
	return tools
}

var stripKeys = map[string]bool{
	"description": true,
	"title":       true,
	"examples":    true,
	"default":     true,
}

func stripSchema(props map[string]any) map[string]any {
	if props == nil {
		return nil
	}
	out := make(map[string]any, len(props))
	for k, v := range props {
		if stripKeys[k] {
			continue
		}
		if k == "properties" {
			if nested, ok := v.(map[string]any); ok {
				out[k] = stripSchema(nested)
				continue
			}
		}
		if k == "items" {
			if items, ok := v.(map[string]any); ok {
				out[k] = stripSchema(items)
				continue
			}
		}
		m, ok := v.(map[string]any)
		if !ok {
			out[k] = v
			continue
		}
		cleaned := make(map[string]any, len(m))
		for mk, mv := range m {
			if stripKeys[mk] {
				continue
			}
			if mk == "properties" {
				if nested, ok := mv.(map[string]any); ok {
					cleaned[mk] = stripSchema(nested)
					continue
				}
			}
			if mk == "items" {
				if items, ok := mv.(map[string]any); ok {
					cleaned[mk] = stripSchema(items)
					continue
				}
			}
			cleaned[mk] = mv
		}
		out[k] = cleaned
	}
	return out
}

func fromProtoMessages(input []proto.Message) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion
	for _, msg := range input {
		switch msg.Role {
		case proto.RoleSystem:
			messages = append(messages, openai.SystemMessage(msg.Content))
		case proto.RoleTool:
			for _, call := range msg.ToolCalls {
				messages = append(messages, openai.ToolMessage(msg.Content, call.ID))
				break
			}
		case proto.RoleUser:
			if len(msg.Images) > 0 {
				var parts []openai.ChatCompletionContentPartUnionParam
				for _, img := range msg.Images {
					b64 := base64.StdEncoding.EncodeToString(img.Data)
					dataURL := fmt.Sprintf("data:%s;base64,%s", img.MimeType, b64)
					parts = append(parts, openai.ImageContentPart(openai.ChatCompletionContentPartImageImageURLParam{
						URL: dataURL,
					}))
				}
				if msg.Content != "" {
					parts = append(parts, openai.TextContentPart(msg.Content))
				}
				messages = append(messages, openai.UserMessage(parts))
			} else {
				messages = append(messages, openai.UserMessage(msg.Content))
			}
		case proto.RoleAssistant:
			m := openai.AssistantMessage(msg.Content)
			for _, tool := range msg.ToolCalls {
				m.OfAssistant.ToolCalls = append(m.OfAssistant.ToolCalls, openai.ChatCompletionMessageToolCallParam{
					ID: tool.ID,
					Function: openai.ChatCompletionMessageToolCallFunctionParam{
						Arguments: string(tool.Function.Arguments),
						Name:      tool.Function.Name,
					},
				})
			}
			messages = append(messages, m)
		}
	}
	return messages
}

func toProtoMessage(in openai.ChatCompletionMessageParamUnion) proto.Message {
	msg := proto.Message{
		Role: msgRole(in),
	}
	switch content := in.GetContent().AsAny().(type) {
	case *string:
		if content == nil || *content == "" {
			break
		}
		msg.Content = *content
	case *[]openai.ChatCompletionContentPartTextParam:
		if content == nil || len(*content) == 0 {
			break
		}
		for _, c := range *content {
			msg.Content += c.Text
		}
	}
	if msg.Role == proto.RoleAssistant {
		for _, call := range in.OfAssistant.ToolCalls {
			msg.ToolCalls = append(msg.ToolCalls, proto.ToolCall{
				ID: call.ID,
				Function: proto.Function{
					Name:      call.Function.Name,
					Arguments: []byte(call.Function.Arguments),
				},
			})
		}
	}
	return msg
}

func msgRole(in openai.ChatCompletionMessageParamUnion) string {
	if in.OfSystem != nil {
		return proto.RoleSystem
	}
	if in.OfAssistant != nil {
		return proto.RoleAssistant
	}
	if in.OfUser != nil {
		return proto.RoleUser
	}
	if in.OfTool != nil {
		return proto.RoleTool
	}
	return ""
}
