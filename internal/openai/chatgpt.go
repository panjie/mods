package openai

import (
	"encoding/json"

	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"
	"github.com/panjie/mods/internal/proto"
)

// Subscription function tools must be grouped in a namespace. Tool names
// within the namespace retain the registry names used by mods dispatch.
func chatGPTTools(specs []proto.ToolSpec) []responses.ToolUnionParam {
	var functions, other []responses.ToolUnionParam
	for _, tool := range fromResponseToolSpecs(specs) {
		if tool.OfFunction != nil {
			functions = append(functions, tool)
		} else {
			other = append(other, tool)
		}
	}
	if len(functions) > 0 {
		raw, _ := json.Marshal(map[string]any{"type": "namespace", "name": "mods", "description": "Tools executed locally by mods with user authorization.", "tools": functions})
		other = append(other, param.Override[responses.ToolUnionParam](json.RawMessage(raw)))
	}
	return other
}
