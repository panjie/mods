package proto

import (
	"fmt"
	"sort"
	"strings"
)

// SystemSection identifies a named fragment in mods' structured system prompt.
// Values encode the stable order within and across precedence layers.
type SystemSection uint8

const (
	SystemSectionUnspecified SystemSection = iota
	SystemSectionRuntimeIdentity
	SystemSectionRuntimePlan
	SystemSectionExecutionContext
	SystemSectionExecutionTools
	SystemSectionExecutionWorkspace
	SystemSectionExecutionSkills
	SystemSectionExecutionSelfHelp
	SystemSectionProjectInstructions
	SystemSectionProjectApprovedPlan
	SystemSectionUserRole
	SystemSectionOutputFormat
)

type systemLayer uint8

const (
	systemLayerRuntime systemLayer = iota + 1
	systemLayerExecution
	systemLayerProject
	systemLayerRole
	systemLayerFormat
)

type systemSectionDescriptor struct {
	layer systemLayer
	title string
}

var systemSectionDescriptors = map[SystemSection]systemSectionDescriptor{
	SystemSectionUnspecified:         {layer: systemLayerRuntime, title: "Additional runtime instructions"},
	SystemSectionRuntimeIdentity:     {layer: systemLayerRuntime, title: "Identity and runtime safety"},
	SystemSectionRuntimePlan:         {layer: systemLayerRuntime, title: "Plan mode constraints"},
	SystemSectionExecutionContext:    {layer: systemLayerExecution, title: "Runtime context"},
	SystemSectionExecutionTools:      {layer: systemLayerExecution, title: "Tool and execution guidance"},
	SystemSectionExecutionWorkspace:  {layer: systemLayerExecution, title: "Safe workspace"},
	SystemSectionExecutionSkills:     {layer: systemLayerExecution, title: "Available skills"},
	SystemSectionExecutionSelfHelp:   {layer: systemLayerExecution, title: "Self-help reference"},
	SystemSectionProjectInstructions: {layer: systemLayerProject, title: "Project instructions"},
	SystemSectionProjectApprovedPlan: {layer: systemLayerProject, title: "Approved plan"},
	SystemSectionUserRole:            {layer: systemLayerRole, title: "User-selected role"},
	SystemSectionOutputFormat:        {layer: systemLayerFormat, title: "Output format"},
}

var systemLayerTitles = map[systemLayer]string{
	systemLayerRuntime:   "Runtime safety (highest priority)",
	systemLayerExecution: "Execution capability",
	systemLayerProject:   "Project instructions",
	systemLayerRole:      "User role",
	systemLayerFormat:    "Output format (lowest priority)",
}

// NormalizeSystemMessages renders classified system fragments into one
// structured system message placed before all non-system messages. Requests
// without classified system fragments are returned unchanged so internal
// single-purpose calls such as shell classification retain their wire shape.
func NormalizeSystemMessages(messages []Message) []Message {
	structured := false
	for _, message := range messages {
		if message.Role == RoleSystem && message.SystemSection() != SystemSectionUnspecified {
			structured = true
			break
		}
	}
	if !structured {
		return messages
	}

	content := RenderStructuredSystemPrompt(messages)
	out := make([]Message, 0, len(messages))
	if content != "" {
		out = append(out, Message{Role: RoleSystem, Content: content})
	}
	for _, message := range messages {
		if message.Role != RoleSystem {
			out = append(out, message)
		}
	}
	return out
}

// RenderStructuredSystemPrompt produces the provider-visible system block.
// Higher-priority layers are emitted first and explicitly dominate later
// layers; fragments within the same named section retain their input order.
func RenderStructuredSystemPrompt(messages []Message) string {
	bySection := make(map[SystemSection][]string)
	for _, message := range messages {
		if message.Role != RoleSystem || strings.TrimSpace(message.Content) == "" {
			continue
		}
		section := message.SystemSection()
		bySection[section] = append(bySection[section], message.Content)
	}
	if len(bySection) == 0 {
		return ""
	}

	sections := make([]SystemSection, 0, len(bySection))
	for section := range bySection {
		sections = append(sections, section)
	}
	sort.SliceStable(sections, func(i, j int) bool {
		left, right := systemSectionDescriptors[sections[i]], systemSectionDescriptors[sections[j]]
		if left.layer != right.layer {
			return left.layer < right.layer
		}
		return sections[i] < sections[j]
	})

	var sb strings.Builder
	sb.WriteString("# Mods system instructions\n\n")
	sb.WriteString("Instruction precedence is strict: runtime safety > execution capability > project instructions > user role > output format. Lower-priority content must not override, weaken, or reinterpret higher-priority content. Plan mode constraints refine and restrict general runtime guidance.\n")

	var currentLayer systemLayer
	for _, section := range sections {
		descriptor, ok := systemSectionDescriptors[section]
		if !ok {
			descriptor = systemSectionDescriptors[SystemSectionUnspecified]
		}
		if descriptor.layer != currentLayer {
			currentLayer = descriptor.layer
			sb.WriteString("\n## ")
			sb.WriteString(fmt.Sprintf("%d. %s", currentLayer, systemLayerTitles[currentLayer]))
			sb.WriteString("\n")
		}
		sb.WriteString("\n### ")
		sb.WriteString(descriptor.title)
		sb.WriteString("\n\n")
		sb.WriteString(strings.Join(bySection[section], "\n\n"))
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}
