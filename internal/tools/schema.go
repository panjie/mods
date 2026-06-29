package tools

import (
	"encoding/json"
	"fmt"
)

// Shared JSON-schema helpers used by every Register* function in this
// package. Kept here so the per-tool files can focus on tool behaviour
// rather than schema construction.

// decodeArgs unmarshals tool arguments, treating an empty payload as
// "leave args at its zero value" so tools with optional input succeed.
func decodeArgs(data json.RawMessage, args any) error {
	if len(data) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, args); err != nil {
		return fmt.Errorf("invalid tool arguments: %w: %s", err, string(data))
	}
	return nil
}

func objectSchema(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func stringProp(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"description": description,
	}
}

func integerProp(description string) map[string]any {
	return map[string]any{
		"type":        "integer",
		"description": description,
	}
}
