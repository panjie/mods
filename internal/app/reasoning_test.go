package app

import (
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

func TestApplyReasoningConfigs(t *testing.T) {
	// ── thinking.type providers (MiniMax / GLM / Anthropic-compat) ──

	t.Run("MiniMax: thinking disabled in extra-params gets flipped to adaptive (default thinking-type)", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "disabled"},
			},
		}

		applyReasoningConfigs(Model{API: "minimax"}, nil, nil, &ccfg)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget, "MiniMax adaptive must not have budget_tokens")
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("MiniMax: thinking with user-set budget_tokens has budget_tokens stripped for adaptive", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{
					"type":          "disabled",
					"budget_tokens": 4096,
				},
			},
		}

		applyReasoningConfigs(Model{API: "minimax"}, nil, nil, &ccfg)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget)
	})

	t.Run("GLM: thinking-type=enabled auto-creates thinking block when extra-params.thinking is absent", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg)

		thinking, ok := ccfg.ExtraParams["thinking"].(map[string]any)
		require.True(t, ok, "thinking block should be auto-created")
		require.Equal(t, "enabled", thinking["type"])
		require.True(t, ccfg.ThinkTags, "ThinkTags should be enabled for inline tag parsing")
	})

	t.Run("GLM: thinking-type=enabled flips existing thinking.type to enabled and preserves budget_tokens", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{
					"type":          "disabled",
					"budget_tokens": 4096,
				},
			},
		}

		applyReasoningConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, 4096, thinking["budget_tokens"], "budget_tokens must be preserved for non-adaptive types")
	})

	// ── reasoning_effort providers (OpenAI / DeepSeek) ──

	t.Run("reasoning_effort pinned in extra-params gets upgraded to configured effort", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"reasoning_effort": "low",
			},
		}

		applyReasoningConfigs(Model{API: "custom", ReasoningEffort: "high"}, nil, nil, &ccfg)

		require.Equal(t, "high", ccfg.ExtraParams["reasoning_effort"])
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("no extra-params falls back to OpenAI body field with default medium effort", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "openai"}, nil, nil, &ccfg)

		require.Equal(t, openai.ReasoningEffortMedium, ccfg.ReasoningEffort)
		require.Nil(t, ccfg.ExtraParams)
	})

	t.Run("no extra-params uses model-level reasoning-effort when set", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "openai", ReasoningEffort: "high"}, nil, nil, &ccfg)

		require.Equal(t, "high", string(ccfg.ReasoningEffort))
	})

	// ── native anthropic / google / cohere / ollama ──

	t.Run("anthropic api path uses ThinkingBudget and ignores extra-params", func(t *testing.T) {
		accfg := anthropic.Config{}
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "disabled"},
			},
		}

		applyReasoningConfigs(Model{API: "anthropic"}, nil, &accfg, &ccfg)

		require.Equal(t, 8192, accfg.ThinkingBudget)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("google api path uses ThinkingBudget", func(t *testing.T) {
		gccfg := google.Config{}
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &ccfg)

		require.Equal(t, 8192, gccfg.ThinkingBudget)
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("google ThinkingBudget set at model level is preserved", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 2048}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{})

		require.Equal(t, 2048, gccfg.ThinkingBudget)
	})

	t.Run("cohere and ollama skip reasoning entirely", func(t *testing.T) {
		for _, api := range []string{"cohere", "ollama"} {
			ccfg := openai.Config{
				ExtraParams: map[string]any{
					"thinking": map[string]any{"type": "disabled"},
				},
			}

			applyReasoningConfigs(Model{API: api}, nil, nil, &ccfg)

			require.Empty(t, ccfg.ReasoningEffort)
			require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
		}
	})

	// ── ThinkTags always enabled for openai-compatible providers ──

	t.Run("ThinkTags enabled for all openai-compatible providers in default branch", func(t *testing.T) {
		for _, api := range []string{"openai", "minimax", "glm", "custom"} {
			ccfg := openai.Config{}
			applyReasoningConfigs(Model{API: api}, nil, nil, &ccfg)
			require.True(t, ccfg.ThinkTags, api+": ThinkTags should be enabled")
		}
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
