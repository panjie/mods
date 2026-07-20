package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/responses"
	"github.com/panjie/mods/internal/proto"
)

const responsesProviderDataKey = "openai.responses.output"

func fromResponseToolSpecs(specs []proto.ToolSpec) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	for _, spec := range specs {
		params := proto.StripSchema(spec.InputSchema)
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		tool := responses.FunctionToolParam{
			Name:        spec.Name,
			Description: openai.String(spec.Description),
			Parameters:  params,
			Strict:      openai.Bool(false),
		}
		tools = append(tools, responses.ToolUnionParam{OfFunction: &tool})
	}
	return tools
}

func fromProtoResponseInput(input []proto.Message) (responses.ResponseInputParam, error) {
	input = proto.NormalizeSystemMessages(input)
	var items responses.ResponseInputParam
	for _, msg := range input {
		if state := msg.ProviderData[responsesProviderDataKey]; len(state) > 0 {
			replayed, err := responseProviderItems(state)
			if err != nil {
				return nil, err
			}
			items = append(items, replayed...)
			continue
		}

		switch msg.Role {
		case proto.RoleSystem:
			items = append(items, responses.ResponseInputItemParamOfMessage(
				msg.Content,
				responses.EasyInputMessageRoleSystem,
			))
		case proto.RoleUser:
			if len(msg.Images) == 0 {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					msg.Content,
					responses.EasyInputMessageRoleUser,
				))
				continue
			}
			var content responses.ResponseInputMessageContentListParam
			for _, img := range msg.Images {
				b64 := base64.StdEncoding.EncodeToString(img.Data)
				dataURL := fmt.Sprintf("data:%s;base64,%s", img.MimeType, b64)
				part := responses.ResponseInputContentParamOfInputImage(
					responses.ResponseInputImageDetailAuto,
				)
				part.OfInputImage.ImageURL = openai.String(dataURL)
				content = append(content, part)
			}
			if msg.Content != "" {
				content = append(content, responses.ResponseInputContentParamOfInputText(msg.Content))
			}
			items = append(items, responses.ResponseInputItemParamOfMessage(
				content,
				responses.EasyInputMessageRoleUser,
			))
		case proto.RoleAssistant:
			if msg.Content != "" {
				items = append(items, responses.ResponseInputItemParamOfMessage(
					msg.Content,
					responses.EasyInputMessageRoleAssistant,
				))
			}
			for _, call := range msg.ToolCalls {
				items = append(items, responses.ResponseInputItemParamOfFunctionCall(
					string(call.Function.Arguments),
					call.ID,
					call.Function.Name,
				))
			}
		case proto.RoleTool:
			for _, call := range msg.ToolCalls {
				items = append(items, responses.ResponseInputItemParamOfFunctionCallOutput(
					call.ID,
					msg.Content,
				))
				break
			}
		}
	}
	return items, nil
}

func responseProviderItems(state json.RawMessage) ([]responses.ResponseInputItemUnionParam, error) {
	var rawItems []json.RawMessage
	if err := json.Unmarshal(state, &rawItems); err != nil {
		return nil, fmt.Errorf("decode saved OpenAI response output: %w", err)
	}
	items := make([]responses.ResponseInputItemUnionParam, 0, len(rawItems))
	for _, raw := range rawItems {
		var item responses.ResponseInputItemUnion
		if err := json.Unmarshal(raw, &item); err != nil {
			return nil, fmt.Errorf("decode saved OpenAI response item: %w", err)
		}
		items = append(items, item.ToParam())
	}
	return items, nil
}

func responseToProtoMessage(response responses.Response, streamedContent string) (proto.Message, error) {
	msg := proto.Message{
		Role:    proto.RoleAssistant,
		Content: streamedContent,
	}
	var rawItems []json.RawMessage
	for _, item := range response.Output {
		raw := item.RawJSON()
		if raw == "" {
			encoded, err := json.Marshal(item)
			if err != nil {
				return proto.Message{}, fmt.Errorf("encode OpenAI response item: %w", err)
			}
			raw = string(encoded)
		}
		rawItems = append(rawItems, json.RawMessage(raw))
		if item.Type == "function_call" {
			msg.ToolCalls = append(msg.ToolCalls, proto.ToolCall{
				ID: item.CallID,
				Function: proto.Function{
					Name:      item.Name,
					Arguments: []byte(item.Arguments),
				},
			})
		}
	}
	if msg.Content == "" {
		msg.Content = responseVisibleText(response)
	}
	if len(rawItems) > 0 {
		state, err := json.Marshal(rawItems)
		if err != nil {
			return proto.Message{}, fmt.Errorf("encode OpenAI response output: %w", err)
		}
		msg.ProviderData = map[string]json.RawMessage{
			responsesProviderDataKey: state,
		}
	}
	return msg, nil
}

func responseVisibleText(response responses.Response) string {
	var text string
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			switch content.Type {
			case "output_text":
				text += content.Text
			case "refusal":
				text += content.Refusal
			}
		}
	}
	return text
}
