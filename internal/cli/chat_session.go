package cli

import (
	"context"

	cfgpkg "github.com/panjie/mods/internal/config"
)

// chatSession owns invocation state; it never writes back to parsed CLI flags.
// Approval full control and MCP pooling retain their existing context lifetimes.
type chatSession struct {
	options           cfgpkg.InvocationOptions
	initial           cfgpkg.TurnInput
	id                string
	started           bool
	instructionsShown bool
}
type chatSessionKey struct{}
type turnConfigKey struct{}

func (s *chatSession) next(ctx context.Context, prompt string) context.Context {
	input := cfgpkg.TurnInput{Prompt: RemoveWhitespace(prompt)}
	if !s.started {
		input = s.initial
		input.Prompt = RemoveWhitespace(prompt)
	}
	cfg := s.options.ForTurn(input, s.id)
	cfg.ProjectInstructionsNoticeShown = s.instructionsShown
	return context.WithValue(ctx, turnConfigKey{}, &cfg)
}
func turnConfig(ctx context.Context) *Config {
	if cfg, ok := ctx.Value(turnConfigKey{}).(*Config); ok {
		return cfg
	}
	return &config
}
func resultConfig(mods *Mods) *Config {
	if mods != nil && mods.Config != nil {
		return mods.Config
	}
	return &config
}
