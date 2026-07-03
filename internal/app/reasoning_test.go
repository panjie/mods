package app

import (
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

func TestResolveReasoning(t *testing.T) {
	t.Run("on enables reasoning", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Reasoning: ReasoningOn}}}
		ccfg := openai.Config{}

		active, err := m.resolveReasoning(&Model{API: "openai"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.True(t, active)
		require.Equal(t, openai.ReasoningEffortMedium, ccfg.ReasoningEffort)
	})

	t.Run("off disables reasoning", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Reasoning: ReasoningOff}}}
		ccfg := openai.Config{}

		active, err := m.resolveReasoning(&Model{API: "openai"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.False(t, active)
		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
	})
}

func TestApplyReasoningConfigs(t *testing.T) {
	// ── thinking.type providers (MiniMax / GLM / Anthropic-compat) ──

	t.Run("MiniMax: thinking disabled in extra-params gets flipped to adaptive (default thinking-type)", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "disabled"},
			},
		}

		applyReasoningConfigs(Model{API: "minimax"}, nil, nil, &ccfg, true)

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

		applyReasoningConfigs(Model{API: "minimax"}, nil, nil, &ccfg, true)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget)
	})

	t.Run("GLM: thinking-type=enabled auto-creates thinking block when extra-params.thinking is absent", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

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

		applyReasoningConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, 4096, thinking["budget_tokens"], "budget_tokens must be preserved for non-adaptive-types")
	})

	// ── reasoning_effort providers (OpenAI / DeepSeek) ──

	t.Run("reasoning_effort pinned in extra-params gets upgraded to configured effort", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"reasoning_effort": "low",
			},
		}

		applyReasoningConfigs(Model{API: "custom", ReasoningEffort: "high"}, nil, nil, &ccfg, true)

		require.Equal(t, "high", ccfg.ExtraParams["reasoning_effort"])
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("no extra-params falls back to OpenAI body field with default medium effort", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "openai"}, nil, nil, &ccfg, true)

		require.Equal(t, openai.ReasoningEffortMedium, ccfg.ReasoningEffort)
		require.Nil(t, ccfg.ExtraParams)
	})

	t.Run("no extra-params uses model-level reasoning-effort when set", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "openai", ReasoningEffort: "high"}, nil, nil, &ccfg, true)

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

		applyReasoningConfigs(Model{API: "anthropic"}, nil, &accfg, &ccfg, true)

		require.Equal(t, 8192, accfg.ThinkingBudget)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("google api path uses ThinkingBudget", func(t *testing.T) {
		gccfg := google.Config{}
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &ccfg, true)

		require.Equal(t, 8192, gccfg.ThinkingBudget)
		require.Empty(t, ccfg.ReasoningEffort)
	})

	t.Run("google ThinkingBudget set at model level is preserved", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 2048}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{}, true)

		require.Equal(t, 2048, gccfg.ThinkingBudget)
	})

	t.Run("cohere and ollama skip reasoning entirely", func(t *testing.T) {
		for _, api := range []string{"cohere", "ollama"} {
			ccfg := openai.Config{
				ExtraParams: map[string]any{
					"thinking": map[string]any{"type": "disabled"},
				},
			}

			applyReasoningConfigs(Model{API: api}, nil, nil, &ccfg, true)

			require.Empty(t, ccfg.ReasoningEffort)
			require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
		}
	})

	// ── ThinkTags enabled for openai-compatible providers when reasoning on ──

	t.Run("ThinkTags enabled for all openai-compatible providers in default branch", func(t *testing.T) {
		for _, api := range []string{"openai", "minimax", "glm", "custom"} {
			ccfg := openai.Config{}
			applyReasoningConfigs(Model{API: api}, nil, nil, &ccfg, true)
			require.True(t, ccfg.ThinkTags, api+": ThinkTags should be enabled")
		}
	})
}

func TestApplyReasoningConfigsDisable(t *testing.T) {
	// ── thinking.type style: disabled when -r is off ──

	t.Run("thinking-type model sends thinking.type=disabled when -r off", func(t *testing.T) {
		// GLM/Kimi/DeepSeek have thinking-type set but no thinking block in extra-params.
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg, false)

		thinking, ok := ccfg.ExtraParams["thinking"].(map[string]any)
		require.True(t, ok, "thinking block should be created to send disabled")
		require.Equal(t, "disabled", thinking["type"])
		require.False(t, ccfg.ThinkTags, "ThinkTags should be off when reasoning is off")
	})

	t.Run("model with existing thinking block gets type overwritten to disabled", func(t *testing.T) {
		// MiniMax-style: config carries thinking.type=disabled already; code must
		// still set it (idempotent) so the off-state is guaranteed.
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"thinking": map[string]any{"type": "enabled"},
			},
		}

		applyReasoningConfigs(Model{API: "minimax", ThinkingType: "adaptive"}, nil, nil, &ccfg, false)

		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "disabled", thinking["type"])
	})

	// ── enable_thinking style (Qwen): flipped to false ──

	t.Run("enable_thinking model sends false when -r off", func(t *testing.T) {
		ccfg := openai.Config{
			ExtraParams: map[string]any{
				"enable_thinking": true,
			},
		}

		applyReasoningConfigs(Model{API: "qwen"}, nil, nil, &ccfg, false)

		require.Equal(t, false, ccfg.ExtraParams["enable_thinking"])
	})

	// ── Google Gemini: thinkingBudget=0 + Explicit ──

	t.Run("google sends thinkingBudget=0 and Explicit flag when -r off", func(t *testing.T) {
		gccfg := google.Config{}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{}, false)

		require.Equal(t, 0, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit, "Explicit flag must be set so 0 is actually sent")
	})

	t.Run("google with previously-set budget gets reset to 0 when -r off", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 8192}

		applyReasoningConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{}, false)

		require.Equal(t, 0, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	// ── Anthropic: off is a no-op (default is already off) ──

	t.Run("anthropic off is a no-op (no thinking budget set)", func(t *testing.T) {
		accfg := anthropic.Config{}

		applyReasoningConfigs(Model{API: "anthropic"}, nil, &accfg, nil, false)

		require.Equal(t, 0, accfg.ThinkingBudget, "Anthropic defaults to thinking off; off-path should not change anything")
	})

	// ── OpenAI reasoning_effort: cannot fully disable, sends minimal ──

	t.Run("openai sends reasoning_effort=minimal when -r off", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "openai"}, nil, nil, &ccfg, false)

		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
	})

	t.Run("azure sends reasoning_effort=minimal when -r off", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "azure"}, nil, nil, &ccfg, false)

		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
	})

	// ── unknown provider without thinking config: no-op, no crash ──

	t.Run("unknown provider without thinking config is a no-op", func(t *testing.T) {
		ccfg := openai.Config{}

		applyReasoningConfigs(Model{API: "custom"}, nil, nil, &ccfg, false)

		require.Empty(t, ccfg.ExtraParams)
	})
}
