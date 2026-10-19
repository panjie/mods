package proto

import (
	"encoding/json"
	"fmt"
	"sort"
)

// PromptSize measures provider-neutral instruction and tool-definition bytes.
// It excludes conversation content and media. It is neither a token estimate
// nor the size of a provider's final wire encoding. Section body bytes and tool
// description/schema bytes are breakdowns, not additions to the total.
type PromptSize struct {
	SystemBytes          int                 `json:"system_bytes"`
	ToolDescriptionBytes int                 `json:"tool_description_bytes"`
	ToolSchemaBytes      int                 `json:"tool_schema_json_bytes"`
	ToolDefinitionsBytes int                 `json:"tool_definitions_json_bytes"`
	Sections             []SystemSectionSize `json:"sections,omitempty"`
	Tools                []ToolSize          `json:"tools,omitempty"`
}

type SystemSectionSize struct {
	Section SystemSection `json:"section"`
	Title   string        `json:"title"`
	Bytes   int           `json:"body_bytes"`
}

type ToolSize struct {
	Name             string `json:"name"`
	DescriptionBytes int    `json:"description_bytes"`
	SchemaBytes      int    `json:"schema_json_bytes"`
}

func (s PromptSize) TotalBytes() int {
	return s.SystemBytes + s.ToolDefinitionsBytes
}

// MeasurePromptSize includes the rendered system hierarchy and complete tool
// definitions, including schema property descriptions and JSON overhead.
func MeasurePromptSize(messages []Message, tools []ToolSpec) (PromptSize, error) {
	var size PromptSize
	for _, message := range NormalizeSystemMessages(messages) {
		if message.Role == RoleSystem {
			size.SystemBytes += len(message.Content)
		}
	}
	sections := make(map[SystemSection]int)
	for _, message := range messages {
		if message.Role == RoleSystem && message.Content != "" {
			sections[message.SystemSection()] += len(message.Content)
		}
	}
	for section, bytes := range sections {
		descriptor, ok := systemSectionDescriptors[section]
		if !ok {
			descriptor = systemSectionDescriptors[SystemSectionUnspecified]
		}
		size.Sections = append(size.Sections, SystemSectionSize{Section: section, Title: descriptor.title, Bytes: bytes})
	}
	sort.Slice(size.Sections, func(i, j int) bool { return size.Sections[i].Section < size.Sections[j].Section })
	if len(tools) == 0 {
		return size, nil
	}
	for _, tool := range tools {
		schema, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return PromptSize{}, fmt.Errorf("measure tool %q schema: %w", tool.Name, err)
		}
		entry := ToolSize{Name: tool.Name, DescriptionBytes: len(tool.Description), SchemaBytes: len(schema)}
		size.Tools = append(size.Tools, entry)
		size.ToolDescriptionBytes += entry.DescriptionBytes
		size.ToolSchemaBytes += entry.SchemaBytes
	}
	definitions, err := json.Marshal(tools)
	if err != nil {
		return PromptSize{}, fmt.Errorf("measure tool definitions: %w", err)
	}
	size.ToolDefinitionsBytes = len(definitions)
	return size, nil
}
