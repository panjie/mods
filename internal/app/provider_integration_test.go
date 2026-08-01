//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/panjie/mods/internal/config"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	"github.com/stretchr/testify/require"
)

type providerTest struct {
	api     string
	model   string
	envKey  string
	baseURL string
}

var providerTests = []providerTest{
	{api: "openai", model: "gpt-4o-mini", envKey: "OPENAI_API_KEY", baseURL: "https://api.openai.com/v1"},
	{api: "google", model: "gemini-2.5-flash", envKey: "GOOGLE_API_KEY"},
	{api: "anthropic", model: "claude-3-5-haiku-20241022", envKey: "ANTHROPIC_API_KEY", baseURL: "https://api.anthropic.com/v1"},
	{api: "deepseek", model: "deepseek-v4-flash", envKey: "DEEPSEEK_API_KEY", baseURL: "https://api.deepseek.com/"},
	{api: "ollama", model: "llama3.1"},
}

func testIntegrationMods(t *testing.T, api, model string) *Mods {
	t.Helper()
	return testIntegrationModsWithBaseURL(t, api, model, "")
}

func testIntegrationModsWithBaseURL(t *testing.T, api, model, baseURL string) *Mods {
	t.Helper()
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
		Styles: makeStyles(true),
		Config: &Config{
			PersistentConfig: config.PersistentConfig{
				Model:      model,
				API:        api,
				APIs:       apis,
				Format:     "markdown",
				MCPTimeout: 30 * time.Second,
				MaxRetries: 1,
			},
		},
	}
}

func TestOpenAIIntegration(t *testing.T) {
	skipIfNoKey(t, "OPENAI_API_KEY", "openai")
	m := testIntegrationModsWithBaseURL(t, "openai", "gpt-4o-mini", "https://api.openai.com/v1")
	runIntegrationPrompt(t, m, "hello")
}

func TestGoogleIntegration(t *testing.T) {
	skipIfNoKey(t, "GOOGLE_API_KEY", "google")
	m := testIntegrationMods(t, "google", "gemini-2.5-flash")
	runIntegrationPrompt(t, m, "say hello")
}

func TestAnthropicIntegration(t *testing.T) {
	skipIfNoKey(t, "ANTHROPIC_API_KEY", "anthropic")
	m := testIntegrationModsWithBaseURL(t, "anthropic", "claude-3-5-haiku-20241022", "https://api.anthropic.com/v1")
	runIntegrationPrompt(t, m, "say hello")
}

func TestDeepSeekResponsesIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "deepseek")
	m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
	m.Config.Think = true
	m.Config.ShowTokenUsage = true
	runIntegrationPrompt(t, m, "say hello")
}

func TestDeepSeekChatIntegration(t *testing.T) {
	skipIfNoKey(t, "DEEPSEEK_API_KEY", "deepseek")
	m := testIntegrationModsWithBaseURL(t, "deepseek", "deepseek-v4-flash", "https://api.deepseek.com/")
	model := m.Config.APIs[0].Models["deepseek-v4-flash"]
	model.Endpoint = "chat-completions"
	m.Config.APIs[0].Models["deepseek-v4-flash"] = model
	m.Config.Think = true
	m.Config.ShowTokenUsage = true
	runIntegrationPrompt(t, m, "say hello")
}

func TestOllamaIntegration(t *testing.T) {
	m := testIntegrationMods(t, "ollama", "llama3.1")
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
	output, ok := msg.(streamEventMsg)
	if !ok {
		merr, isErr := msg.(modsError)
		if isErr {
			reason := merr.ReasonText
			if merr.Err != nil {
				reason = merr.Err.Error()
			}
			t.Fatalf("expected streamEventMsg, got modsError: %s", reason)
		}
		t.Fatalf("expected streamEventMsg, got %T: %v", msg, msg)
	}
	require.Equal(t, streamEventChunk, output.kind)

	var fullText strings.Builder
	var fullThought strings.Builder
	fullText.WriteString(output.chunk.Content)
	fullThought.WriteString(output.chunk.Thought)
	runner := output.runner
	for {
		msg := runner.receiveCmd()()
		event, ok := msg.(streamEventMsg)
		if !ok || event.kind != streamEventChunk {
			break
		}
		if event.chunk.Content != "" {
			fullText.WriteString(event.chunk.Content)
		}
		if event.chunk.Thought != "" {
			fullThought.WriteString(event.chunk.Thought)
		}
	}
	require.NotEmpty(t, fullText.String(), "expected non-empty streamed content from %s API", m.Config.API)
	if m.Config.Think {
		require.NotEmpty(t, fullThought.String(), "expected streamed reasoning from %s API", m.Config.API)
	}
	if err := runner.stream.Err(); err != nil && !errors.Is(err, stream.ErrNoContent) {
		t.Logf("stream ended with error: %v", err)
	}
	if m.Config.ShowTokenUsage {
		require.True(t, runner.stream.Usage().Available(), "expected token usage from %s API", m.Config.API)
	}
	runner.close()

	messages := runner.messages()
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
