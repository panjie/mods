package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/openai/openai-go"
	"github.com/panjie/mods/internal/google"
	api "github.com/panjie/mods/internal/ollamaapi"
)

func (m *Mods) handleRequestError(err error, mod Model, content string) tea.Msg {
	ae := &openai.Error{}
	if errors.As(err, &ae) {
		debug.Printf("API error: HTTP %d, code=%q, message=%q", ae.StatusCode, ae.Code, ae.Message)
		return m.handleAPIError(ae, mod, content)
	}
	ge := &google.APIError{}
	if errors.As(err, &ge) {
		debug.Printf("Google API error: HTTP %d, message=%q", ge.StatusCode, ge.Message)
		return m.handleGoogleAPIError(ge, mod, content)
	}
	anth := &anthropic.Error{}
	if errors.As(err, &anth) {
		debug.Printf("Anthropic API error: HTTP %d, message=%q", anth.StatusCode, anth.Error())
		return m.handleAnthropicAPIError(anth, mod, content)
	}
	se := &api.StatusError{}
	if errors.As(err, &se) {
		debug.Printf("Ollama API error: HTTP %d, message=%q", se.StatusCode, se.Error())
		return m.handleOllamaAPIError(se, mod, content)
	}
	oe := &api.AuthorizationError{}
	if errors.As(err, &oe) {
		debug.Printf("Ollama auth error: HTTP %d", oe.StatusCode)
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	}
	debug.Printf("Request error (non-API): %v", err)
	return modsError{
		Err: err,
		ReasonText: fmt.Sprintf(
			"There was a problem with the %s API request.",
			mod.API,
		),
	}
}

// appAPIError is the SDK-agnostic shape of a provider API error. Each
// provider handler below extracts the relevant fields from its SDK-specific
// error type into this struct so the per-status dispatch logic can be
// shared. Message is the most descriptive user-facing string available
// (the SDK's Message field when one exists, otherwise Error()).
type appAPIError struct {
	StatusCode int
	Code       string
	Message    string
	Err        error
}

// errorPolicy carries the per-provider differences that dispatchAPIError
// cannot derive from the status code alone. Keeping these as data (or thin
// closures) lets the dispatch skeleton be shared instead of duplicated
// across five near-identical handlers.
type errorPolicy struct {
	// isContextLength reports whether the error indicates the prompt
	// exceeded the model's context window. Return false (or set to nil)
	// to disable the CutPrompt retry path entirely (e.g. ollama).
	isContextLength func(ae appAPIError) bool
	// notFoundExtra is appended to the standard 404-no-fallback reason
	// text. Empty for most providers; ollama uses it to mention
	// `ollama pull`.
	notFoundExtra string
	// badRequestReason renders the 400-non-context-length reason text.
	// Some providers include the underlying message; OpenAI does not.
	badRequestReason func(mod Model, ae appAPIError) string
	// serverError handles HTTP 500 specifically. OpenAI historically
	// branched on the API name here (retry for openai, hard fail for
	// OpenAI-compatible providers); other providers just retry like any
	// other 5xx. Return nil to fall back to the default 5xx retry path.
	serverError func(m *Mods, ae appAPIError, mod Model, content string) tea.Msg
}

// dispatchAPIError contains the unified per-status handling for all
// provider errors. The five exported handlers below are thin translators
// that build an appAPIError and an errorPolicy from their SDK types and
// delegate here.
func (m *Mods) dispatchAPIError(ae appAPIError, mod Model, content string, p errorPolicy) tea.Msg {
	cfg := m.Config
	switch ae.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        ae.Err,
				ReasonText: fmt.Sprintf("%s API model not found.", mod.API),
			})
		}
		return modsError{Err: ae.Err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'.%s",
			cfg.Model, cfg.API, p.notFoundExtra,
		)}
	case http.StatusBadRequest:
		if p.isContextLength != nil && p.isContextLength(ae) {
			pe := modsError{Err: ae.Err, ReasonText: "Maximum prompt size exceeded."}
			if cfg.NoLimit {
				return pe
			}
			return m.retry(CutPrompt(ae.Message, content), pe)
		}
		return modsError{Err: ae.Err, ReasonText: p.badRequestReason(mod, ae)}
	case http.StatusUnauthorized:
		return modsError{Err: ae.Err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		return m.retry(content, modsError{
			Err: ae.Err, ReasonText: fmt.Sprintf("You've hit your %s API rate limit.", mod.API),
		})
	case http.StatusRequestEntityTooLarge:
		// Previously OpenAI-only; the maintainability review (H1)
		// extended it to every provider since the message is generic.
		return modsError{Err: ae.Err, ReasonText: fmt.Sprintf(
			"Request too large for %s API. Try reducing input size, removing images, or using %s.",
			mod.API,
			m.Styles.InlineCode.Render("--no-limit=false"),
		)}
	case http.StatusServiceUnavailable:
		return m.retry(content, modsError{
			Err:        ae.Err,
			ReasonText: fmt.Sprintf("%s API is temporarily unavailable (HTTP 503).", mod.API),
		})
	case http.StatusInternalServerError:
		if p.serverError != nil {
			if msg := p.serverError(m, ae, mod, content); msg != nil {
				return msg
			}
		}
		return m.retry(content, modsError{
			Err: ae.Err,
			ReasonText: fmt.Sprintf(
				"%s API server error (HTTP %d): %s",
				mod.API, ae.StatusCode, ae.Message,
			),
		})
	default:
		if ae.StatusCode >= 500 {
			return m.retry(content, modsError{
				Err: ae.Err,
				ReasonText: fmt.Sprintf(
					"%s API server error (HTTP %d): %s",
					mod.API, ae.StatusCode, ae.Message,
				),
			})
		}
		return modsError{Err: ae.Err, ReasonText: fmt.Sprintf(
			"%s API request error (HTTP %d): %s", mod.API, ae.StatusCode, ae.Message,
		)}
	}
}

func (m *Mods) handleAPIError(err *openai.Error, mod Model, content string) tea.Msg {
	return m.dispatchAPIError(
		appAPIError{StatusCode: err.StatusCode, Code: err.Code, Message: err.Message, Err: err},
		mod, content,
		errorPolicy{
			isContextLength: func(ae appAPIError) bool {
				return ae.Code == "context_length_exceeded"
			},
			badRequestReason: func(mod Model, _ appAPIError) string {
				return fmt.Sprintf("%s API request error.", mod.API)
			},
			serverError: func(mm *Mods, ae appAPIError, mod Model, content string) tea.Msg {
				// OpenAI's own endpoints occasionally 500 in a way that
				// recovers on retry. OpenAI-compatible providers that 500
				// almost always mean "model not loaded", so they fail fast
				// instead of burning retries.
				if mod.API == "openai" {
					return mm.retry(content, modsError{
						Err: ae.Err, ReasonText: "OpenAI API server error.",
					})
				}
				return modsError{Err: ae.Err, ReasonText: fmt.Sprintf(
					"Error loading model '%s' for API '%s'.", mod.Name, mod.API,
				)}
			},
		},
	)
}

func (m *Mods) handleGoogleAPIError(err *google.APIError, mod Model, content string) tea.Msg {
	return m.dispatchAPIError(
		appAPIError{StatusCode: err.StatusCode, Message: err.Message, Err: err},
		mod, content,
		errorPolicy{
			isContextLength: func(ae appAPIError) bool {
				return strings.Contains(ae.Message, "context length") ||
					strings.Contains(ae.Message, "token count")
			},
			badRequestReason: func(mod Model, ae appAPIError) string {
				return fmt.Sprintf("%s API request error: %s", mod.API, ae.Message)
			},
		},
	)
}

func (m *Mods) handleAnthropicAPIError(err *anthropic.Error, mod Model, content string) tea.Msg {
	return m.dispatchAPIError(
		appAPIError{StatusCode: err.StatusCode, Message: err.Error(), Err: err},
		mod, content,
		errorPolicy{
			isContextLength: func(ae appAPIError) bool {
				return strings.Contains(ae.Message, "prompt is too long") ||
					strings.Contains(ae.Message, "number of tokens")
			},
			badRequestReason: func(mod Model, ae appAPIError) string {
				return fmt.Sprintf("%s API request error: %s", mod.API, ae.Message)
			},
		},
	)
}

func (m *Mods) handleOllamaAPIError(err *api.StatusError, mod Model, content string) tea.Msg {
	return m.dispatchAPIError(
		appAPIError{StatusCode: err.StatusCode, Message: err.Error(), Err: err},
		mod, content,
		errorPolicy{
			// ollama does not surface a context-length signal.
			isContextLength: nil,
			notFoundExtra:   " Check that the model is pulled with `ollama pull`.",
			badRequestReason: func(mod Model, ae appAPIError) string {
				return fmt.Sprintf("%s API request error: %s", mod.API, ae.Message)
			},
		},
	)
}
