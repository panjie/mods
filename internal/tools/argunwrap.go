package tools

import (
	"encoding/json"
	"strings"
)

// argumentWrapperKeys are the single-property keys under which weak models
// sometimes nest a tool call's real parameters, mirroring the wire envelopes
// of OpenAI-style function calls ("arguments") and MCP tool requests
// ("arguments", "parameters").
var argumentWrapperKeys = []string{"arguments", "parameters"}

// UnwrapToolArguments reverses a common weak-model mistake: nesting the real
// tool parameters under a lone "arguments" (or "parameters") key, sometimes
// double-encoded as a JSON string:
//
//	{"arguments": {"question": "…"}}       -> {"question": "…"}
//	{"arguments": "{\"question\": \"…\"}"} -> {"question": "…"}
//
// The payload is returned unchanged unless it is a JSON object with exactly
// one property whose key is a known wrapper key. A schema that declares the
// wrapper key as a legitimate top-level property (possible for MCP tools)
// opts out entirely so real calls are never rewritten.
func UnwrapToolArguments(schema map[string]any, data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil || len(object) != 1 {
		return data
	}
	for _, key := range argumentWrapperKeys {
		raw, wrapped := object[key]
		if !wrapped || schemaDeclaresProperty(schema, key) {
			continue
		}
		if unwrapped := objectArgumentValue(raw); unwrapped != nil {
			return unwrapped
		}
	}
	return data
}

// objectArgumentValue returns the JSON object carried by a wrapper value,
// accepting either a direct object or a string that encodes one. It returns
// nil for anything else.
func objectArgumentValue(raw json.RawMessage) []byte {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return nil
	}
	switch v := value.(type) {
	case map[string]any:
		return raw
	case string:
		trimmed := strings.TrimSpace(v)
		if !strings.HasPrefix(trimmed, "{") {
			return nil
		}
		var nested map[string]any
		if err := json.Unmarshal([]byte(trimmed), &nested); err != nil {
			return nil
		}
		return []byte(trimmed)
	default:
		return nil
	}
}

// schemaDeclaresProperty reports whether the tool's input schema exposes key
// as a top-level property, meaning the wrapper key is legitimate there.
func schemaDeclaresProperty(schema map[string]any, key string) bool {
	if schema == nil {
		return false
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		return false
	}
	_, declared := properties[key]
	return declared
}
