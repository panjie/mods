package providerinfo

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenCodeGoRoutesRequireOfficialService(t *testing.T) {
	for _, base := range []string{
		"https://opencode.ai/zen/go/v1", "https://OPENCODE.AI/zen/go/v1/",
		"https://opencode.ai/inference/go/v1", DefaultBaseURL("opencode-go"),
		DefaultBaseURL("opencode-go-anthropic"),
	} {
		for _, id := range []string{"gpt-6-luna", "gpt-5.6-luna", "grok-4.7", "muse-spark-1.2-contributor", "muse-spark-1.3-contributor"} {
			route, ok := OpenCodeGoRoute(base, id)
			require.True(t, ok, "%s %s", base, id)
			require.Equal(t, ModelRoute{"openai", "responses"}, route)
		}
	}
	for _, base := range []string{
		"https://gateway.example/v1", "https://opencode.ai.example.com/zen/go/v1",
		"http://opencode.ai/zen/go/v1", "https://opencode.ai/zen/v1",
		"https://opencode.ai/other/v1", "https://opencode.ai:8443/zen/go/v1",
		"https://user@opencode.ai/zen/go/v1", "https://opencode.ai/zen/go/v1?x=1",
		"https://opencode.ai/zen/go/v1#fragment", "https://opencode.ai/zen/%67o/v1", "://bad",
	} {
		_, ok := OpenCodeGoRoute(base, "gpt-6-luna")
		require.False(t, ok, base)
	}
	base := DefaultBaseURL("opencode-go")
	_, ok := OpenCodeGoRoute(base, "gpt-6-luna-new")
	require.False(t, ok)
	route, ok := OpenCodeGoRoute(base, "deepseek-v4-pro")
	require.True(t, ok)
	require.Equal(t, ModelRoute{"openai", "chat-completions"}, route)
	route, ok = OpenCodeGoRoute(base, "qwen3.8-max")
	require.True(t, ok)
	require.Equal(t, ModelRoute{"anthropic", "messages"}, route)
}

func TestOpenCodeGoEndpointPrecedence(t *testing.T) {
	base := DefaultBaseURL("opencode-go")
	require.Equal(t, "responses", OpenAIEndpoint("alias", "gpt-6-luna", base, ""))
	require.Equal(t, "chat-completions", OpenAIEndpoint("opencode-go", "gpt-6-luna", base, " Chat-Completions "))
	require.Equal(t, "chat-completions", OpenAIEndpoint("opencode-go", "gpt-6-luna", "https://custom.example/v1", ""))
	require.Equal(t, "chat-completions", OpenAIEndpoint("alias", "unknown", base, ""))
	require.Equal(t, "openai", Profile("opencode-go", "", ""))
}
