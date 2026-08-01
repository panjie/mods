package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/shared/constant"
	"github.com/panjie/mods/internal/proto"
)

const deepSeekChatProviderDataKey = "deepseek.chat.assistant.v1"

type deepSeekChatAssistantState struct {
	ReasoningContent string `json:"reasoning_content"`
}

func fromToolSpecs(specs []proto.ToolSpec) []openai.ChatCompletionToolParam {
	var tools []openai.ChatCompletionToolParam
	for _, spec := range specs {
		params := proto.StripSchema(spec.InputSchema)
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

func fromProtoMessages(input []proto.Message) []openai.ChatCompletionMessageParamUnion {
	return fromProtoMessagesForProfile(input, ProviderProfileOpenAI)
}

func fromProtoMessagesForProfile(input []proto.Message, profile ProviderProfile) []openai.ChatCompletionMessageParamUnion {
	input = proto.NormalizeSystemMessages(input)
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
			if profile == ProviderProfileDeepSeek {
				m = deepSeekAssistantMessage(m, msg.ProviderData[deepSeekChatProviderDataKey])
			}
			messages = append(messages, m)
		}
	}
	return messages
}

func deepSeekAssistantMessage(
	message openai.ChatCompletionMessageParamUnion,
	stateJSON json.RawMessage,
) openai.ChatCompletionMessageParamUnion {
	if len(stateJSON) == 0 {
		return message
	}
	var state deepSeekChatAssistantState
	if err := json.Unmarshal(stateJSON, &state); err != nil || state.ReasoningContent == "" {
		return message
	}
	raw, err := json.Marshal(message)
	if err != nil {
		return message
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		return message
	}
	fields["reasoning_content"] = state.ReasoningContent
	raw, err = json.Marshal(fields)
	if err != nil {
		return message
	}
	return param.Override[openai.ChatCompletionMessageParamUnion](json.RawMessage(raw))
}

func attachDeepSeekChatState(msg *proto.Message, reasoning string) {
	if msg == nil || reasoning == "" {
		return
	}
	state, err := json.Marshal(deepSeekChatAssistantState{ReasoningContent: reasoning})
	if err != nil {
		return
	}
	if msg.ProviderData == nil {
		msg.ProviderData = map[string]json.RawMessage{}
	}
	msg.ProviderData[deepSeekChatProviderDataKey] = state
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
