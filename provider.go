package main

import (
	"fmt"

	"github.com/charmbracelet/mods/internal/anthropic"
	"github.com/charmbracelet/mods/internal/cohere"
	"github.com/charmbracelet/mods/internal/google"
	"github.com/charmbracelet/mods/internal/ollama"
	"github.com/charmbracelet/mods/internal/openai"
	"github.com/charmbracelet/mods/internal/stream"
)

// newStreamClient creates the appropriate stream.Client for the given API
// backend. This consolidates the provider switch that was duplicated in
// startCompletionCmd and judgeTaskComplexity.
func newStreamClient(api string, accfg anthropic.Config, gccfg google.Config,
	cccfg cohere.Config, occfg ollama.Config, ccfg openai.Config,
) (stream.Client, error) {
	switch api {
	case "anthropic":
		return anthropic.New(accfg), nil
	case "google":
		return google.New(gccfg), nil
	case "cohere":
		return cohere.New(cccfg), nil
	case "ollama":
		c, err := ollama.New(occfg)
		if err != nil {
			return nil, fmt.Errorf("ollama: %w", err)
		}
		return c, nil
	default:
		return openai.New(ccfg), nil
	}
}
