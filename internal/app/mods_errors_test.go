package app

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/require"
)

func testMods(t *testing.T) *Mods {
	t.Helper()
	r := lipgloss.NewRenderer(nil)
	return &Mods{
		Styles: makeStyles(r),
		Config: &Config{
			PersistentConfig: PersistentConfig{
				Model: "gpt-4",
				API:   "openai",
			},
		},
	}
}

func TestHandleAPIError(t *testing.T) {
	makeErr := func(status int, code string) *openai.Error {
		return &openai.Error{
			StatusCode: status,
			Code:       code,
			Message:    fmt.Sprintf("HTTP %d test error", status),
		}
	}

	t.Run("404 with fallback retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		apiErr := makeErr(http.StatusNotFound, "")
		mod := Model{Name: "gpt-4", API: "openai", Fallback: "gpt-3.5-turbo"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		ci, ok := msg.(completionInput)
		require.True(t, ok, "expected completionInput for retry, got %T", msg)
		require.Equal(t, "prompt", ci.content)
	})

	t.Run("404 without fallback returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusNotFound, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Missing model")
	})

	t.Run("400 context_length_exceeded retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		m.Config.NoLimit = false
		apiErr := makeErr(http.StatusBadRequest, "context_length_exceeded")
		apiErr.Message = "This model's maximum context length is 10 tokens. However, your messages resulted in 3 tokens"
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "this is a long prompt I have no idea if its really 10 tokens")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected completionInput for retry")
	})

	t.Run("400 context_length_exceeded no_limit returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.NoLimit = true
		apiErr := makeErr(http.StatusBadRequest, "context_length_exceeded")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Maximum prompt size exceeded")
	})

	t.Run("400 other returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusBadRequest, "invalid_request")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
	})

	t.Run("401 returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusUnauthorized, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("429 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		apiErr := makeErr(http.StatusTooManyRequests, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected completionInput for rate limit retry")
	})

	t.Run("413 returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusRequestEntityTooLarge, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Request too large")
	})

	t.Run("500 openai retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		apiErr := makeErr(http.StatusInternalServerError, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected completionInput for 500 retry")
	})

	t.Run("500 non-openai returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusInternalServerError, "")
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Error loading model")
	})

	t.Run("4xx client error returns error", func(t *testing.T) {
		m := testMods(t)
		apiErr := makeErr(http.StatusTeapot, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
	})

	t.Run("5xx unknown retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		apiErr := makeErr(http.StatusBadGateway, "")
		mod := Model{Name: "gpt-4", API: "anthropic"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected completionInput for 5xx retry")
	})

	t.Run("max retries reached returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 5
		m.retries = 10
		apiErr := makeErr(http.StatusInternalServerError, "")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleAPIError(apiErr, mod, "prompt")
		_, ok := msg.(modsError)
		require.True(t, ok, "expected modsError when max retries reached")
	})
}

func TestHandleRequestError(t *testing.T) {
	t.Run("openai error delegates to handleAPIError", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		apiErr := &openai.Error{
			StatusCode: http.StatusUnauthorized,
			Message:    "invalid key",
		}
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleRequestError(apiErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("non-openai error returns generic message", func(t *testing.T) {
		m := testMods(t)
		otherErr := fmt.Errorf("network timeout")
		mod := Model{Name: "gpt-4", API: "openai"}
		msg := m.handleRequestError(otherErr, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request")
	})
}
