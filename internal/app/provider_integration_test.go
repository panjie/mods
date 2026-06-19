//go:build integration

package app

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	"github.com/stretchr/testify/require"
)

type providerTest struct {
	api     string
	model   string
	envKey  string
	baseURL string
}

var providerTests = []providerTest{
	{api: "openai", model: "gpt-5.4", envKey: "OPENAI_API_KEY", baseURL: "https://api.openai.com/v1"},
	{api: "google", model: "gemini-3.5-flash", envKey: "GOOGLE_API_KEY"},
	{api: "anthropic", model: "claude-sonnet-4-6", envKey: "ANTHROPIC_API_KEY", baseURL: "https://api.anthropic.com/v1"},
	{api: "cohere", model: "command-a-v7", envKey: "COHERE_API_KEY"},
	{api: "ollama", model: "llama4:16x17b"},
}

func testIntegrationMods(t *testing.T, api, model string) *Mods {
	t.Helper()
	return testIntegrationModsWithBaseURL(t, api, model, "")
}

func testIntegrationModsWithBaseURL(t *testing.T, api, model, baseURL string) *Mods {
	t.Helper()
	r := lipgloss.NewRenderer(nil)
	apis := []config.API{
		{
			Name:   api,
			Models: map[string]config.Model{},
		},
	}
	if baseURL != "" {
		apis[0].BaseURL = baseURL
	}
	apis[0].Models[model] = config.Model{
		Name: model,
		API:  api,
	}
	return &Mods{
		ctx:    context.Background(),
		Styles: makeStyles(r),
		Config: &Config{
			PersistentConfig: config.PersistentConfig{
				Model:      model,
				API:        api,
				APIs:       apis,
				FormatAs:   "markdown",
				MCPTimeout: 30 * time.Second,
				MaxRetries: 1,
				StatusText: "Generating",
			},
		},
	}
}

func TestOpenAIIntegration(t *testing.T) {
	skipIfNoKey(t, "OPENAI_API_KEY", "openai")
	m := testIntegrationModsWithBaseURL(t, "openai", "gpt-5.4", "https://api.openai.com/v1")
	runIntegrationPrompt(t, m, "hello")
}

func TestGoogleIntegration(t *testing.T) {
	skipIfNoKey(t, "GOOGLE_API_KEY", "google")
	m := testIntegrationMods(t, "google", "gemini-3.5-flash")
	runIntegrationPrompt(t, m, "say hello")
}

func TestAnthropicIntegration(t *testing.T) {
	skipIfNoKey(t, "ANTHROPIC_API_KEY", "anthropic")
	m := testIntegrationModsWithBaseURL(t, "anthropic", "claude-sonnet-4-6", "https://api.anthropic.com/v1")
	runIntegrationPrompt(t, m, "say hello")
}

func TestCohereIntegration(t *testing.T) {
	skipIfNoKey(t, "COHERE_API_KEY", "cohere")
	m := testIntegrationMods(t, "cohere", "command-a-v7")
	runIntegrationPrompt(t, m, "say hello")
}

func TestOllamaIntegration(t *testing.T) {
	m := testIntegrationMods(t, "ollama", "llama4:16x17b")
	m.Config.APIs[0].BaseURL = ollamaBaseURL()
	runIntegrationPrompt(t, m, "say hello")
}

func skipIfNoKey(t *testing.T, envVar, provider string) {
	t.Helper()
	if os.Getenv(envVar) == "" {
		t.Skipf("set %s to run %s integration test", envVar, provider)
	}
}

func ollamaBaseURL() string {
	if u := os.Getenv("OLLAMA_HOST"); u != "" {
		return u
	}
	return "http://localhost:11434"
}

func runIntegrationPrompt(t *testing.T, m *Mods, prompt string) {
	t.Helper()
	m.Input = prompt

	msg := m.startCompletionCmd(prompt)()
	output, ok := msg.(completionOutput)
	if !ok {
		merr, isErr := msg.(modsError)
		if isErr {
			reason := merr.ReasonText
			if merr.Err != nil {
				reason = merr.Err.Error()
			}
			t.Fatalf("expected completionOutput, got modsError: %s", reason)
		}
		t.Fatalf("expected completionOutput, got %T: %v", msg, msg)
	}
	require.NotEmpty(t, output.content, "expected non-empty first chunk from %s API", m.Config.API)

	var fullText strings.Builder
	fullText.WriteString(output.content)
	stream := output.stream
	for stream.Next() {
		chunk, err := stream.Current()
		if err != nil {
			break
		}
		if chunk.Content != "" {
			fullText.WriteString(chunk.Content)
		}
	}
	if err := stream.Err(); err != nil {
		t.Logf("stream ended with error: %v", err)
	}
	_ = stream.Close()

	messages := stream.Messages()
	hasAssistant := false
	for _, msg := range messages {
		if msg.Role == proto.RoleAssistant && msg.Content != "" {
			hasAssistant = true
			break
		}
	}
	require.True(t, hasAssistant, "expected at least one assistant message in stream")
	t.Logf("%s response (%d chars): %s...", m.Config.API, fullText.Len(), truncate(fullText.String(), 120))
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
