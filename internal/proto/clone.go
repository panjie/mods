package proto

import (
	"encoding/json"
	"slices"
)

func CloneMessages(messages []Message) []Message {
	cloned := slices.Clone(messages)
	for i := range cloned {
		message := &cloned[i]
		message.Media = slices.Clone(message.Media)
		for j := range message.Media {
			message.Media[j].Data = slices.Clone(message.Media[j].Data)
		}
		message.Images = slices.Clone(message.Images)
		for j := range message.Images {
			message.Images[j].Data = slices.Clone(message.Images[j].Data)
		}
		message.ToolCalls = slices.Clone(message.ToolCalls)
		for j := range message.ToolCalls {
			message.ToolCalls[j].Function.Arguments = slices.Clone(message.ToolCalls[j].Function.Arguments)
		}
		if message.ProviderData != nil {
			message.ProviderData = make(map[string]json.RawMessage, len(message.ProviderData))
			for key, value := range messages[i].ProviderData {
				message.ProviderData[key] = slices.Clone(value)
			}
		}
	}
	return cloned
}
