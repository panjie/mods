package openai

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/panjie/mods/internal/proto"
)

const (
	legacyResponsesProviderDataKey   = "openai.responses.output"
	openAIResponsesProviderDataKey   = "openai.responses.output.v1"
	deepSeekResponsesProviderDataKey = "deepseek.responses.output.v1"
	// responsesProviderDataKey is retained for older package tests and saved
	// sessions. New messages use a profile-specific versioned key.
	responsesProviderDataKey = legacyResponsesProviderDataKey
)

func responsesProviderDataKeyForProfile(profile ProviderProfile) string {
	if profile == ProviderProfileDeepSeek {
		return deepSeekResponsesProviderDataKey
	}
	return openAIResponsesProviderDataKey
}

func fromResponseToolSpecs(specs []proto.ToolSpec) []responses.ToolUnionParam {
	var tools []responses.ToolUnionParam
	for _, spec := range specs {
		wireName := spec.WireName
		if wireName == "" {
			wireName = spec.Name
		}
		switch spec.WireType {
		case "custom":
			raw, _ := json.Marshal(map[string]any{"type": "custom", "name": wireName})
			tools = append(tools, param.Override[responses.ToolUnionParam](json.RawMessage(raw)))
			continue
		case "web_search":
			raw := json.RawMessage(`{"type":"web_search"}`)
			tools = append(tools, param.Override[responses.ToolUnionParam](raw))
			continue
		}
		params := proto.StripSchema(spec.InputSchema)
		if params == nil {
			params = map[string]any{"type": "object"}
		}
		tool := responses.FunctionToolParam{
			Name:        wireName,
			Description: openai.String(spec.Description),
			Parameters:  params,
			Strict:      openai.Bool(false),
		}
		tools = append(tools, responses.ToolUnionParam{OfFunction: &tool})
	}
	return tools
}

func fromProtoResponseInput(input []proto.Message, profile ProviderProfile) (responses.ResponseInputParam, error) {
	input = proto.NormalizeSystemMessages(input)
	var items responses.ResponseInputParam
	providerDataKey := responsesProviderDataKeyForProfile(profile)
	for _, msg := range input {
		state := msg.ProviderData[providerDataKey]
		if len(state) == 0 {
			state = msg.ProviderData[legacyResponsesProviderDataKey]
		}
		if len(state) > 0 {
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
				if call.Type == "custom" {
					raw, marshalErr := json.Marshal(map[string]any{
						"type":    "custom_tool_call_output",
						"call_id": call.ID,
						"output":  msg.Content,
					})
					if marshalErr != nil {
						return nil, fmt.Errorf("encode custom tool output: %w", marshalErr)
					}
					var item responses.ResponseInputItemUnion
					if unmarshalErr := json.Unmarshal(raw, &item); unmarshalErr != nil {
						return nil, fmt.Errorf("encode custom tool output item: %w", unmarshalErr)
					}
					items = append(items, item.ToParam())
					break
				}
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

func responseToProtoMessage(response responses.Response, streamedContent string, profile ProviderProfile) (proto.Message, error) {
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
		} else if item.Type == "custom_tool_call" {
			call, parseErr := customToolCallFromRaw(item.RawJSON())
			if parseErr != nil {
				return proto.Message{}, parseErr
			}
			msg.ToolCalls = append(msg.ToolCalls, proto.ToolCall{
				ID:   call.CallID,
				Type: "custom",
				Function: proto.Function{
					Name:      canonicalCustomToolName(call.Name),
					Arguments: customToolArguments(call.Name, call.Input),
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
			responsesProviderDataKeyForProfile(profile): state,
		}
	}
	return msg, nil
}

type responseCustomToolCall struct {
	CallID string `json:"call_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}

func customToolCallFromRaw(raw string) (responseCustomToolCall, error) {
	var call responseCustomToolCall
	if err := json.Unmarshal([]byte(raw), &call); err != nil {
		return call, fmt.Errorf("decode Responses custom tool call: %w", err)
	}
	if call.CallID == "" || call.Name == "" {
		return call, fmt.Errorf("decode Responses custom tool call: call_id and name are required")
	}
	return call, nil
}

func canonicalCustomToolName(name string) string {
	if name == "apply_patch" {
		return "fs_apply_patch"
	}
	return name
}

func customToolArguments(name, input string) []byte {
	if name != "apply_patch" {
		return []byte(input)
	}
	data, _ := json.Marshal(map[string]string{"patch": input})
	return data
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
