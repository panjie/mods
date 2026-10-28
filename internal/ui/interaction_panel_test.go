package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestInteractionProgressAndGroupedRows(t *testing.T) {
	styles := MakeStyles(true).Interaction
	progress := ansi.Strip(RenderInteractionPanel(styles, 40, InteractionPanel{
		Title:    "Setup",
		Progress: InteractionProgress{Current: 1, Total: 4},
	}))
	require.Contains(t, progress, "━", "progress bar must mark completed work")
	require.Contains(t, progress, "─", "progress bar must keep the remaining cells visible")

	groups := []InteractionGroup{
		{Title: "Provider", Rows: []InteractionRow{{Label: "API", Value: "ollama"}, {Label: "Default model", Value: "llama3.1"}}},
		{Title: "Storage", Rows: []InteractionRow{{Label: "Config file", Value: "/tmp/mods.yml"}}},
	}
	lines := RenderInteractionGroups(styles, 60, groups)
	plain := ansi.Strip(strings.Join(lines, "\n"))
	require.Contains(t, plain, "Provider")
	require.Contains(t, plain, "Storage")
	apiLine := lineContaining(plain, "ollama")
	modelLine := lineContaining(plain, "llama3.1")
	configLine := lineContaining(plain, "/tmp/mods.yml")
	require.NotEmpty(t, apiLine)
	require.NotEmpty(t, modelLine)
	require.NotEmpty(t, configLine)
	valueColumn := func(line, value string) int { return strings.Index(line, value) }
	require.Equal(t, valueColumn(apiLine, "ollama"), valueColumn(modelLine, "llama3.1"))
	require.Equal(t, valueColumn(apiLine, "ollama"), valueColumn(configLine, "/tmp/mods.yml"))
}

func lineContaining(text, needle string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	return ""
}

func TestStackedChoiceUsesCursor(t *testing.T) {
	styles := MakeStyles(true).Interaction
	rendered := ansi.Strip(RenderInteractionPanel(styles, 60, InteractionPanel{
		Title:        "Review",
		Choices:      []InteractionAction{{Key: "Y", Label: "Allow once", Selected: true}, {Key: "N", Label: "Deny"}},
		StackChoices: true,
	}))
	require.Contains(t, rendered, "❯")
	require.Contains(t, rendered, "Y Allow once")
	require.Contains(t, rendered, "N Deny")
}
