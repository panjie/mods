package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/panjie/mods/internal/google"
	"github.com/cohere-ai/cohere-go/v2/core"
	"github.com/ollama/ollama/api"
	"github.com/openai/openai-go"
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
	co := &core.APIError{}
	if errors.As(err, &co) {
		debug.Printf("Cohere API error: HTTP %d, message=%q", co.StatusCode, co.Error())
		return m.handleCohereAPIError(co, mod, content)
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
	return modsError{err, fmt.Sprintf(
		"There was a problem with the %s API request.",
		mod.API,
	)}
}

func (m *Mods) handleAPIError(err *openai.Error, mod Model, content string) tea.Msg {
	cfg := m.Config
	switch err.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API server error.", mod.API),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'.",
			cfg.Model,
			cfg.API,
		)}
	case http.StatusBadRequest:
		if err.Code == "context_length_exceeded" {
			pe := modsError{Err: err, ReasonText: "Maximum prompt size exceeded."}
			if cfg.NoLimit {
				return pe
			}

			return m.retry(CutPrompt(err.Message, content), pe)
		}
		// bad request (do not retry)
		return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error.", mod.API)}
	case http.StatusUnauthorized:
		// invalid auth or key (do not retry)
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		// rate limiting or engine overload (wait and retry)
		return m.retry(content, modsError{
			Err: err, ReasonText: fmt.Sprintf("You’ve hit your %s API rate limit.", mod.API),
		})
	case http.StatusRequestEntityTooLarge:
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Request too large for %s API. Try reducing input size, removing images, or using %s.",
			mod.API,
			m.Styles.InlineCode.Render("--no-limit=false"),
		)}
	case http.StatusInternalServerError:
		if mod.API == "openai" {
			return m.retry(content, modsError{Err: err, ReasonText: "OpenAI API server error."})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Error loading model '%s' for API '%s'.",
			mod.Name,
			mod.API,
		)}
	default:
		if err.StatusCode >= 400 && err.StatusCode < 500 {
			return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error (HTTP %d).", mod.API, err.StatusCode)}
		}
		return m.retry(content, modsError{Err: err, ReasonText: "Unknown API error."})
	}
}

func (m *Mods) handleGoogleAPIError(err *google.APIError, mod Model, content string) tea.Msg {
	switch err.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API model not found.", mod.API),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'.", m.Config.Model, m.Config.API,
		)}
	case http.StatusBadRequest:
		if strings.Contains(err.Message, "context length") || strings.Contains(err.Message, "token count") {
			pe := modsError{Err: err, ReasonText: "Maximum prompt size exceeded."}
			if m.Config.NoLimit {
				return pe
			}
			return m.retry(CutPrompt(err.Message, content), pe)
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error: %s", mod.API, err.Message)}
	case http.StatusUnauthorized:
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		return m.retry(content, modsError{
			Err: err, ReasonText: fmt.Sprintf("You've hit your %s API rate limit.", mod.API),
		})
	case http.StatusServiceUnavailable:
		return m.retry(content, modsError{
			Err:        err,
			ReasonText: fmt.Sprintf("%s API is temporarily unavailable (HTTP 503).", mod.API),
		})
	default:
		if err.StatusCode >= 500 {
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API server error (HTTP %d): %s", mod.API, err.StatusCode, err.Message),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"%s API request error (HTTP %d): %s", mod.API, err.StatusCode, err.Message,
		)}
	}
}

func (m *Mods) handleAnthropicAPIError(err *anthropic.Error, mod Model, content string) tea.Msg {
	switch err.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API model not found.", mod.API),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'.", m.Config.Model, m.Config.API,
		)}
	case http.StatusBadRequest:
		if strings.Contains(err.Error(), "prompt is too long") || strings.Contains(err.Error(), "number of tokens") {
			pe := modsError{Err: err, ReasonText: "Maximum prompt size exceeded."}
			if m.Config.NoLimit {
				return pe
			}
			return m.retry(CutPrompt(err.Error(), content), pe)
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error: %s", mod.API, err.Error())}
	case http.StatusUnauthorized:
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		return m.retry(content, modsError{
			Err: err, ReasonText: fmt.Sprintf("You've hit your %s API rate limit.", mod.API),
		})
	case http.StatusServiceUnavailable:
		return m.retry(content, modsError{
			Err:        err,
			ReasonText: fmt.Sprintf("%s API is temporarily unavailable (HTTP 503).", mod.API),
		})
	default:
		if err.StatusCode >= 500 {
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API server error (HTTP %d): %s", mod.API, err.StatusCode, err.Error()),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"%s API request error (HTTP %d): %s", mod.API, err.StatusCode, err.Error(),
		)}
	}
}

func (m *Mods) handleCohereAPIError(err *core.APIError, mod Model, content string) tea.Msg {
	switch err.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API model not found.", mod.API),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'.", m.Config.Model, m.Config.API,
		)}
	case http.StatusBadRequest:
		if strings.Contains(err.Error(), "token limit") || strings.Contains(err.Error(), "too many tokens") {
			pe := modsError{Err: err, ReasonText: "Maximum prompt size exceeded."}
			if m.Config.NoLimit {
				return pe
			}
			return m.retry(CutPrompt(err.Error(), content), pe)
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error: %s", mod.API, err.Error())}
	case http.StatusUnauthorized:
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		return m.retry(content, modsError{
			Err: err, ReasonText: fmt.Sprintf("You've hit your %s API rate limit.", mod.API),
		})
	case http.StatusServiceUnavailable:
		return m.retry(content, modsError{
			Err:        err,
			ReasonText: fmt.Sprintf("%s API is temporarily unavailable (HTTP 503).", mod.API),
		})
	default:
		if err.StatusCode >= 500 {
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API server error (HTTP %d): %s", mod.API, err.StatusCode, err.Error()),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"%s API request error (HTTP %d): %s", mod.API, err.StatusCode, err.Error(),
		)}
	}
}

func (m *Mods) handleOllamaAPIError(err *api.StatusError, mod Model, content string) tea.Msg {
	switch err.StatusCode {
	case http.StatusNotFound:
		if mod.Fallback != "" {
			m.Config.Model = mod.Fallback
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API model not found.", mod.API),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"Missing model '%s' for API '%s'. Check that the model is pulled with `ollama pull`.", m.Config.Model, m.Config.API,
		)}
	case http.StatusBadRequest:
		return modsError{Err: err, ReasonText: fmt.Sprintf("%s API request error: %s", mod.API, err.Error())}
	case http.StatusUnauthorized:
		return modsError{Err: err, ReasonText: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		return m.retry(content, modsError{
			Err: err, ReasonText: fmt.Sprintf("You've hit your %s API rate limit.", mod.API),
		})
	case http.StatusServiceUnavailable:
		return m.retry(content, modsError{
			Err:        err,
			ReasonText: fmt.Sprintf("%s API is temporarily unavailable (HTTP 503).", mod.API),
		})
	default:
		if err.StatusCode >= 500 {
			return m.retry(content, modsError{
				Err:        err,
				ReasonText: fmt.Sprintf("%s API server error (HTTP %d): %s", mod.API, err.StatusCode, err.Error()),
			})
		}
		return modsError{Err: err, ReasonText: fmt.Sprintf(
			"%s API request error (HTTP %d): %s", mod.API, err.StatusCode, err.Error(),
		)}
	}
}
