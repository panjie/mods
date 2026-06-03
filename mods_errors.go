package main

import (
	"errors"
	"fmt"
	"net/http"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/openai/openai-go"
)

func (m *Mods) handleRequestError(err error, mod Model, content string) tea.Msg {
	ae := &openai.Error{}
	if errors.As(err, &ae) {
		debugPrintf("API error: HTTP %d, code=%q, message=%q", ae.StatusCode, ae.Code, ae.Message)
		return m.handleAPIError(ae, mod, content)
	}
	debugPrintf("Request error (non-API): %v", err)
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
				err:    err,
				reason: fmt.Sprintf("%s API server error.", mod.API),
			})
		}
		return modsError{err: err, reason: fmt.Sprintf(
			"Missing model '%s' for API '%s'.",
			cfg.Model,
			cfg.API,
		)}
	case http.StatusBadRequest:
		if err.Code == "context_length_exceeded" {
			pe := modsError{err: err, reason: "Maximum prompt size exceeded."}
			if cfg.NoLimit {
				return pe
			}

			return m.retry(cutPrompt(err.Message, content), pe)
		}
		// bad request (do not retry)
		return modsError{err: err, reason: fmt.Sprintf("%s API request error.", mod.API)}
	case http.StatusUnauthorized:
		// invalid auth or key (do not retry)
		return modsError{err: err, reason: fmt.Sprintf("Invalid %s API key.", mod.API)}
	case http.StatusTooManyRequests:
		// rate limiting or engine overload (wait and retry)
		return m.retry(content, modsError{
			err: err, reason: fmt.Sprintf("You’ve hit your %s API rate limit.", mod.API),
		})
	case http.StatusRequestEntityTooLarge:
		return modsError{err: err, reason: fmt.Sprintf(
			"Request too large for %s API. Try reducing input size, removing images, or using %s.",
			mod.API,
			m.Styles.InlineCode.Render("--no-limit=false"),
		)}
	case http.StatusInternalServerError:
		if mod.API == "openai" {
			return m.retry(content, modsError{err: err, reason: "OpenAI API server error."})
		}
		return modsError{err: err, reason: fmt.Sprintf(
			"Error loading model '%s' for API '%s'.",
			mod.Name,
			mod.API,
		)}
	default:
		if err.StatusCode >= 400 && err.StatusCode < 500 {
			return modsError{err: err, reason: fmt.Sprintf("%s API request error (HTTP %d).", mod.API, err.StatusCode)}
		}
		return m.retry(content, modsError{err: err, reason: "Unknown API error."})
	}
}
