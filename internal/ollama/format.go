package ollama

import (
	"encoding/base64"
	"encoding/json"
	"strconv"

	api "github.com/panjie/mods/internal/ollamaapi"
	"github.com/panjie/mods/internal/proto"
)

func fromToolSpecs(specs []proto.ToolSpec) []api.Tool {
	var tools []api.Tool
	for _, spec := range specs {
		t := api.Tool{
			Type:  "function",
			Items: nil,
			Function: api.ToolFunction{
				Name:        spec.Name,
				Description: spec.Description,
			},
		}
		b, _ := json.Marshal(spec.InputSchema)
		_ = json.Unmarshal(b, &t.Function.Parameters)
		tools = append(tools, t)
	}
	return tools
}

func fromProtoMessages(input []proto.Message) []api.Message {
	input = proto.NormalizeSystemMessages(input)
	messages := make([]api.Message, 0, len(input))
	for _, msg := range input {
		messages = append(messages, fromProtoMessage(msg))
	}
	return messages
}

func fromProtoMessage(input proto.Message) api.Message {
	m := api.Message{
		Content: input.Content,
		Role:    input.Role,
	}
	for _, img := range input.Images {
		b64 := base64.StdEncoding.EncodeToString(img.Data)
		m.Images = append(m.Images, api.ImageData(b64))
	}
	for _, call := range input.ToolCalls {
		var args api.ToolCallFunctionArguments
		_ = json.Unmarshal(call.Function.Arguments, &args)
		idx, _ := strconv.Atoi(call.ID)
		m.ToolCalls = append(m.ToolCalls, api.ToolCall{
			Function: api.ToolCallFunction{
				Index:     idx,
				Name:      call.Function.Name,
				Arguments: args,
			},
		})
	}
	return m
}

func toProtoMessage(in api.Message) proto.Message {
	msg := proto.Message{
		Role:    in.Role,
		Content: in.Content,
	}
	for _, call := range in.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, proto.ToolCall{
			ID: strconv.Itoa(call.Function.Index),
			Function: proto.Function{
				Arguments: []byte(call.Function.Arguments.String()),
				Name:      call.Function.Name,
			},
		})
	}
	return msg
}
