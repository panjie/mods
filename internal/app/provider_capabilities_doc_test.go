package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Keep the published protocol matrix tied to the clients used by the real
// tool contract suite. These are adapter features, never model allowlists.
func TestProviderCapabilityDocumentation(t *testing.T) {
	yesNo := func(value bool) string {
		if value {
			return "yes"
		}
		return "no"
	}
	var table strings.Builder
	table.WriteString("<!-- provider-capabilities:start -->\n")
	table.WriteString("| Route | Function tools | Image input | Tool images | Audio input/tools | Video input/tools |\n")
	table.WriteString("|---|---|---|---|---|---|\n")
	m := &Mods{Config: &Config{}}
	for _, route := range providerToolContractRoutes {
		model := Model{API: route.api, Name: "contract-model", Endpoint: route.endpoint}
		cfgs, err := m.buildProviderConfigs(model, API{Name: route.api, APIKey: "fixture", BaseURL: "http://127.0.0.1"})
		require.NoError(t, err)
		client, err := newStreamClient(modelProtocol(model), cfgs.Anthropic, cfgs.Google, cfgs.Ollama, cfgs.OpenAI)
		require.NoError(t, err)
		c := client.Capabilities()
		require.True(t, c.Tools && c.FunctionTools, "%s must participate in the execution contract", route.api)
		fmt.Fprintf(&table, "| %s / %s | %s | %s | %s | %s/%s | %s/%s |\n",
			route.api, route.wire, yesNo(c.FunctionTools), yesNo(c.Images), yesNo(c.ToolImages),
			yesNo(c.Audio), yesNo(c.ToolAudio), yesNo(c.Video), yesNo(c.ToolVideo))
	}
	table.WriteString("<!-- provider-capabilities:end -->")
	doc, err := os.ReadFile("../../docs/providers.md")
	require.NoError(t, err)
	require.Contains(t, strings.ReplaceAll(string(doc), "\r\n", "\n"), table.String(),
		"Update the capability block in docs/providers.md to match the runtime:\n%s", table.String())
}
