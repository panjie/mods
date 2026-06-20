package app

import (
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

func TestApplyReasoningConfigs(t *testing.T) {
	t.Run("anthropic-style thinking disabled in extra-params gets flipped to adaptive (no budget_tokens)", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{
					"type": "disabled",
				},
			},
		}

		applyReasoningConfigs("minimax", nil, nil, &ccfg)

		thinking, ok := ccfg.ExtraParams["thinking"].(map[string]any)
		require.True(t, ok, "thinking should remain in extra-params")
		require.Equal(t, "adaptive", thinking["type"], "thinking.type should be flipped to adaptive (MiniMax's only 'on' value)")
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget, "budget_tokens must not be added — MiniMax's adaptive schema rejects it")
		require.Empty(t, ccfg.ReasoningEffort, "ReasoningEffort body field should stay empty so it does not conflict with extra-params")
	})

	t.Run("anthropic-style thinking with user-set budget_tokens has budget_tokens stripped", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{
					"type":          "disabled",
					"budget_tokens": 4096,
				},
			},
		}

		applyReasoningConfigs("minimax", nil, nil, &ccfg)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget, "user-set budget_tokens must be stripped when flipping to adaptive")
	})

	t.Run("reasoning_effort pinned in extra-params gets upgraded to medium in place", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"reasoning_effort": "low",
			},
		}

		applyReasoningConfigs("custom", nil, nil, &ccfg)

		require.Equal(t, "medium", ccfg.ExtraParams["reasoning_effort"], "extra-params.reasoning_effort should be upgraded to medium")
		require.Empty(t, ccfg.ReasoningEffort, "body ReasoningEffort must stay empty; extra-params would otherwise be silently overridden by WithJSONSet ordering")
	})

	t.Run("no extra-params falls back to OpenAI body field", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs("openai", nil, nil, &ccfg)

		require.Equal(t, openai.ReasoningEffortMedium, ccfg.ReasoningEffort)
		require.Nil(t, ccfg.ExtraParams, "extra-params should not be touched when not present")
	})

	t.Run("anthropic api path uses ThinkingBudget and ignores extra-params", func(t *testing.T) {
		accfg := anthropic.Config{}
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "disabled"},
			},
		}

		applyReasoningConfigs("anthropic", nil, &accfg, &ccfg)

		require.Equal(t, 8192, accfg.ThinkingBudget)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"], "anthropic path must not touch extra-params")
	})

	t.Run("does not add budget_tokens even if user previously had thinking.enabled in extra-params", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{
					"type":          "enabled",
					"budget_tokens": 8192,
				},
			},
		}

		applyReasoningConfigs("minimax", nil, nil, &ccfg)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"], "should be normalized to adaptive")
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget, "legacy budget_tokens from a previous 'enabled' config must be stripped")
	})

	t.Run("google api path uses ThinkingBudget", func(t *testing.T) {
		gccfg := google.Config{}
		ccfg := openai.Config{}

		applyReasoningConfigs("google", &gccfg, nil, &ccfg)

		require.Equal(t, 8192, gccfg.ThinkingBudget)
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("cohere and ollama skip reasoning entirely", func(t *testing.T) {
		for _, api := range []string{"cohere", "ollama"} {
			ccfg := openai.Config{
				ExtraParams: map[string]any{
					"thinking": map[string]any{"type": "disabled"},
				},
			}

			applyReasoningConfigs(api, nil, nil, &ccfg)

			require.Empty(t, ccfg.ReasoningEffort, api+": ReasoningEffort should not be set")
			require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"], api+": extra-params.thinking should not be touched")
		}
	})

	t.Run("google ThinkingBudget set at model level is preserved when reasoning is on", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 2048}

		applyReasoningConfigs("google", &gccfg, nil, &openai.Config{})

		require.Equal(t, 2048, gccfg.ThinkingBudget, "pre-existing model-level thinking-budget must not be overwritten")
	})
}

func TestClearThinkingFromExtraParams(t *testing.T) {
	t.Run("nil map is a no-op", func(t *testing.T) {
		clearThinkingFromExtraParams(nil)
	})

	t.Run("removes thinking and reasoning_effort, keeps other keys", func(t *testing.T) {
		extra := map[string]any{
			"thinking":         map[string]any{"type": "enabled"},
			"reasoning_effort": "low",
			"top_p":            0.9,
		}

		clearThinkingFromExtraParams(extra)

		_, hasThinking := extra["thinking"]
		_, hasEffort := extra["reasoning_effort"]
		require.False(t, hasThinking)
		require.False(t, hasEffort)
		require.Equal(t, 0.9, extra["top_p"], "unrelated extra-params must be preserved")
	})
}
