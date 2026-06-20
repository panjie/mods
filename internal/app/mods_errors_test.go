package app

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/charmbracelet/lipgloss"
	"github.com/cohere-ai/cohere-go/v2/core"
	"github.com/ollama/ollama/api"
	"github.com/openai/openai-go"
	"github.com/panjie/mods/internal/google"
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

	t.Run("google error delegates to handleGoogleAPIError", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleRequestError(&google.APIError{StatusCode: http.StatusUnauthorized, Message: "bad key"}, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("anthropic error delegates to handleAnthropicAPIError", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleRequestError(newAnthropicError(t, http.StatusUnauthorized, ""), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("cohere error delegates to handleCohereAPIError", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleRequestError(core.NewAPIError(http.StatusUnauthorized, nil, errors.New("bad key")), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("ollama error delegates to handleOllamaAPIError", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleRequestError(&api.StatusError{StatusCode: http.StatusUnauthorized, Status: "401", ErrorMessage: "bad key"}, mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})
}

// newAnthropicError constructs an *anthropic.Error safe to pass to the error
// handler. The SDK's Error.Error() dereferences Request and Response, so they
// must be non-nil to avoid a panic in tests. When body is non-empty it is fed
// through UnmarshalJSON so that Error() surfaces the message text (used by the
// context-length substring detection in handleAnthropicAPIError).
func newAnthropicError(t *testing.T, status int, body string) *anthropic.Error {
	t.Helper()
	e := &anthropic.Error{
		StatusCode: status,
		Request:    httptest.NewRequest(http.MethodPost, "/v1/messages", nil),
		Response: &http.Response{
			StatusCode: status,
			Status:     fmt.Sprintf("%d %s", status, http.StatusText(status)),
		},
	}
	if body != "" {
		require.NoError(t, e.UnmarshalJSON([]byte(body)))
	}
	return e
}

func TestHandleGoogleAPIError(t *testing.T) {
	makeErr := func(status int, message string) *google.APIError {
		return &google.APIError{StatusCode: status, Message: message}
	}

	t.Run("404 with fallback retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "gemini-2.5-pro", API: "google", Fallback: "gemini-2.5-flash"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		require.IsType(t, completionInput{}, msg, "expected retry")
		require.Equal(t, "gemini-2.5-flash", m.Config.Model, "fallback model should be selected")
	})

	t.Run("404 without fallback returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Missing model")
	})

	t.Run("400 context length retries with CutPrompt", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		m.Config.NoLimit = false
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusBadRequest, "Request exceeds context length"), mod, "a long prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on context-length error")
	})

	t.Run("400 token count retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusBadRequest, "token count too high"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on token-count error")
	})

	t.Run("400 context length no_limit returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.NoLimit = true
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusBadRequest, "context length exceeded"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Maximum prompt size exceeded")
	})

	t.Run("400 other returns error with message", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusBadRequest, "bad request body"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
		require.Contains(t, merr.ReasonText, "bad request body")
	})

	t.Run("401 returns invalid key error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusUnauthorized, ""), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("429 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusTooManyRequests, ""), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on rate limit")
	})

	t.Run("503 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusServiceUnavailable, ""), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 503")
	})

	t.Run("5xx retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusBadGateway, "upstream down"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 5xx")
	})

	t.Run("4xx unknown returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "gemini-2.5-pro", API: "google"}
		msg := m.handleGoogleAPIError(makeErr(http.StatusTeapot, ""), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
	})
}

func TestHandleAnthropicAPIError(t *testing.T) {
	contextLenBody := `{"type":"error","error":{"type":"invalid_request_error","message":"prompt is too long: 250000 tokens > 200000 maximum"}}`
	tokenCountBody := `{"type":"error","error":{"message":"number of tokens exceeds limit"}}`

	t.Run("404 with fallback retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "claude-sonnet-4", API: "anthropic", Fallback: "claude-3-5-haiku"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusNotFound, ""), mod, "prompt")
		require.IsType(t, completionInput{}, msg, "expected retry")
		require.Equal(t, "claude-3-5-haiku", m.Config.Model)
	})

	t.Run("404 without fallback returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusNotFound, ""), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Missing model")
	})

	t.Run("400 prompt too long retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		m.Config.NoLimit = false
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusBadRequest, contextLenBody), mod, "a long prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on prompt-too-long")
	})

	t.Run("400 number of tokens retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusBadRequest, tokenCountBody), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on number-of-tokens")
	})

	t.Run("400 prompt too long no_limit returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.NoLimit = true
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusBadRequest, contextLenBody), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Maximum prompt size exceeded")
	})

	t.Run("400 other returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusBadRequest, `{"error":{"message":"bad request"}}`), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
	})

	t.Run("401 returns invalid key error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusUnauthorized, ""), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("429 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusTooManyRequests, ""), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on rate limit")
	})

	t.Run("503 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusServiceUnavailable, ""), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 503")
	})

	t.Run("5xx retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "claude-sonnet-4", API: "anthropic"}
		msg := m.handleAnthropicAPIError(newAnthropicError(t, http.StatusBadGateway, ""), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 5xx")
	})
}

func TestHandleCohereAPIError(t *testing.T) {
	makeErr := func(status int, msg string) *core.APIError {
		return core.NewAPIError(status, nil, errors.New(msg))
	}

	t.Run("404 with fallback retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "command-r-plus", API: "cohere", Fallback: "command-r"}
		msg := m.handleCohereAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		require.IsType(t, completionInput{}, msg, "expected retry")
		require.Equal(t, "command-r", m.Config.Model)
	})

	t.Run("404 without fallback returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Missing model")
	})

	t.Run("400 token limit retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		m.Config.NoLimit = false
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusBadRequest, "token limit exceeded"), mod, "a long prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on token-limit")
	})

	t.Run("400 too many tokens retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusBadRequest, "too many tokens in input"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on too-many-tokens")
	})

	t.Run("400 token limit no_limit returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.NoLimit = true
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusBadRequest, "token limit exceeded"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Maximum prompt size exceeded")
	})

	t.Run("400 other returns error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusBadRequest, "invalid request"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "API request error")
	})

	t.Run("401 returns invalid key error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusUnauthorized, "unauthorized"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("429 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusTooManyRequests, "rate limited"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on rate limit")
	})

	t.Run("503 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusServiceUnavailable, "unavailable"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 503")
	})

	t.Run("5xx retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "command-r-plus", API: "cohere"}
		msg := m.handleCohereAPIError(makeErr(http.StatusBadGateway, "gateway down"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 5xx")
	})
}

func TestHandleOllamaAPIError(t *testing.T) {
	makeErr := func(status int, msg string) *api.StatusError {
		return &api.StatusError{StatusCode: status, Status: fmt.Sprintf("%d %s", status, http.StatusText(status)), ErrorMessage: msg}
	}

	t.Run("404 with fallback retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "llama3.3", API: "ollama", Fallback: "llama3.2"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		require.IsType(t, completionInput{}, msg, "expected retry")
		require.Equal(t, "llama3.2", m.Config.Model)
	})

	t.Run("404 without fallback hints to pull model", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusNotFound, "model not found"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Missing model")
		require.Contains(t, merr.ReasonText, "ollama pull")
	})

	t.Run("400 has no context-length branch, returns error", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "llama3.3", API: "ollama"}
		// Ollama handler does not detect context-length; any 400 is a hard error.
		msg := m.handleOllamaAPIError(makeErr(http.StatusBadRequest, "token limit exceeded"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok, "400 must NOT retry for ollama (no context-length handling)")
		require.Contains(t, merr.ReasonText, "API request error")
	})

	t.Run("401 returns invalid key error", func(t *testing.T) {
		m := testMods(t)
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusUnauthorized, "unauthorized"), mod, "prompt")
		merr, ok := msg.(modsError)
		require.True(t, ok)
		require.Contains(t, merr.ReasonText, "Invalid")
	})

	t.Run("429 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusTooManyRequests, "rate limited"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on rate limit")
	})

	t.Run("503 retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusServiceUnavailable, "unavailable"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 503")
	})

	t.Run("5xx retries", func(t *testing.T) {
		m := testMods(t)
		m.Config.MaxRetries = 2
		m.retries = 0
		mod := Model{Name: "llama3.3", API: "ollama"}
		msg := m.handleOllamaAPIError(makeErr(http.StatusBadGateway, "gateway down"), mod, "prompt")
		_, ok := msg.(completionInput)
		require.True(t, ok, "expected retry on 5xx")
	})
}
