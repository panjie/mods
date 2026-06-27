//go:build integration

package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
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
	{api: "cohere", model: "command-r-plus", envKey: "COHERE_API_KEY"},
	{api: "ollama", model: "llama3.1"},
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

func TestCohereIntegration(t *testing.T) {
	skipIfNoKey(t, "COHERE_API_KEY", "cohere")
	m := testIntegrationMods(t, "cohere", "command-r-plus")
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
	require.NotEmpty(t, output.chunk.Content, "expected non-empty first chunk from %s API", m.Config.API)

	var fullText strings.Builder
	fullText.WriteString(output.chunk.Content)
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
	}
	if err := runner.stream.Err(); err != nil && !errors.Is(err, stream.ErrNoContent) {
		t.Logf("stream ended with error: %v", err)
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
