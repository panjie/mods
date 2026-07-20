package app

import (
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

func TestResolveThink(t *testing.T) {
	t.Run("requested enables known provider default", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Think: true}}}
		ccfg := openai.Config{}

		active, err := m.resolveThink(&Model{API: "deepseek", Name: "deepseek-v4-flash"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.True(t, active)
		require.Equal(t, "enabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("requested and thinking-type overrides default", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Think: true}}}
		ccfg := openai.Config{}

		active, err := m.resolveThink(&Model{API: "deepseek", Name: "deepseek-reasoner", ThinkingType: "enabled"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.True(t, active)
		require.Equal(t, "enabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("off disables thinking", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Think: false}}}
		ccfg := openai.Config{}

		active, err := m.resolveThink(&Model{API: "openai", Name: "gpt-5.4-mini-2026-03-17"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.False(t, active)
		require.Equal(t, "none", ccfg.ExtraParams["reasoning_effort"])
	})
}

func TestApplyThinkConfigsDefaults(t *testing.T) {
	t.Run("DeepSeek without thinking-type sends enabled when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "deepseek", Name: "deepseek-v4-flash"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.True(t, ccfg.ThinkTags)
		require.Equal(t, "enabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("DeepSeek with thinking-type sends enabled when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "deepseek", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.True(t, ccfg.ThinkTags)
		require.Equal(t, "enabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("DeepSeek with thinking-type still sends disabled when not requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "deepseek", ThinkingType: "enabled"}, nil, nil, &ccfg, false)

		require.False(t, active)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("MiniMax defaults to adaptive and strips budget_tokens", func(t *testing.T) {
		ccfg := openai.Config{ExtraParams: map[string]any{
			"thinking": map[string]any{"type": "disabled", "budget_tokens": 4096},
		}}

		active := applyThinkConfigs(Model{API: "minimax"}, nil, nil, &ccfg, true)

		require.True(t, active)
		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget)
	})

	t.Run("GLM defaults to enabled and preserves non-adaptive budget_tokens", func(t *testing.T) {
		ccfg := openai.Config{ExtraParams: map[string]any{
			"thinking": map[string]any{"type": "disabled", "budget_tokens": 4096},
		}}

		active := applyThinkConfigs(Model{API: "glm"}, nil, nil, &ccfg, true)

		require.True(t, active)
		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, 4096, thinking["budget_tokens"])
	})

	t.Run("Google without thinking-type sends configured budget when requested", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 2048}

		active := applyThinkConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{}, true)

		require.True(t, active)
		require.Equal(t, 2048, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	t.Run("Google with thinking-type sends configured budget", func(t *testing.T) {
		gccfg := google.Config{}

		active := applyThinkConfigs(Model{API: "google", ThinkingType: "enabled", ThinkingBudget: 4096}, &gccfg, nil, &openai.Config{}, true)

		require.True(t, active)
		require.Equal(t, 4096, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	t.Run("Anthropic without thinking-type sends budget when requested", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(Model{API: "anthropic"}, nil, &accfg, nil, true)

		require.True(t, active)
		require.Equal(t, defaultThinkingBudget, accfg.ThinkingBudget)
	})

	t.Run("Anthropic with thinking-type sends budget", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(Model{API: "anthropic", ThinkingType: "enabled"}, nil, &accfg, nil, true)

		require.True(t, active)
		require.Equal(t, defaultThinkingBudget, accfg.ThinkingBudget)
	})

	t.Run("Qwen without thinking-type sends enable_thinking true when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "qwen"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, true, ccfg.ExtraParams["enable_thinking"])
	})

	t.Run("Qwen with thinking-type sends enable_thinking true", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "qwen", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, true, ccfg.ExtraParams["enable_thinking"])
	})

	t.Run("OpenAI without thinking-type sends reasoning effort when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "openai"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, openai.ReasoningEffortMedium, ccfg.ReasoningEffort)
		require.Empty(t, ccfg.ExtraParams)
	})

	t.Run("unknown OpenAI-compatible provider without thinking-type sends no provider-specific off field", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "groq", Name: "llama"}, nil, nil, &ccfg, true)

		require.False(t, active)
		require.Empty(t, ccfg.ExtraParams)
	})

	t.Run("OpenAI with thinking-type sends reasoning effort", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "openai", ThinkingType: "enabled", ReasoningEffort: "high"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, "high", string(ccfg.ReasoningEffort))
	})

	t.Run("custom provider with off override preserves enabled effort", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{
			API:                "custom",
			Name:               "opaque-deployment",
			ReasoningEffort:    "high",
			ReasoningEffortOff: "none",
		}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, "high", string(ccfg.ReasoningEffort))
		require.Empty(t, ccfg.ExtraParams)
	})
}

func TestApplyThinkConfigsDisable(t *testing.T) {
	tests := []struct {
		name   string
		model  Model
		effort string
	}{
		{
			name:   "GPT-5.4 mini snapshot uses none",
			model:  Model{API: "openai", Name: "gpt-5.4-mini-2026-03-17"},
			effort: "none",
		},
		{
			name:   "GPT-5.1 uses none",
			model:  Model{API: "openai", Name: "gpt-5.1"},
			effort: "none",
		},
		{
			name:   "future versioned GPT-5 family uses none",
			model:  Model{API: "openai", Name: "gpt-5.6-terra"},
			effort: "none",
		},
		{
			name:   "original GPT-5 uses minimal",
			model:  Model{API: "openai", Name: "gpt-5"},
			effort: "minimal",
		},
		{
			name:   "original GPT-5 mini snapshot uses minimal",
			model:  Model{API: "openai", Name: "gpt-5-mini-2025-08-07"},
			effort: "minimal",
		},
		{
			name:   "o-series uses low",
			model:  Model{API: "openai", Name: "o3-2025-04-16"},
			effort: "low",
		},
		{
			name:  "GPT-4o omits effort",
			model: Model{API: "openai", Name: "gpt-4o"},
		},
		{
			name:  "Pro model omits effort",
			model: Model{API: "openai", Name: "gpt-5.4-pro"},
		},
		{
			name:  "opaque Azure deployment omits effort",
			model: Model{API: "azure", Name: "production-deployment"},
		},
		{
			name:   "explicit Azure override is used",
			model:  Model{API: "azure", Name: "production-deployment", ReasoningEffortOff: "none"},
			effort: "none",
		},
		{
			name:   "explicit override wins over automatic selection",
			model:  Model{API: "openai", Name: "gpt-5.4-mini", ReasoningEffortOff: "low"},
			effort: "low",
		},
		{
			name:   "custom provider can opt in with override",
			model:  Model{API: "custom", Name: "opaque-model", ReasoningEffortOff: "minimal"},
			effort: "minimal",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ccfg := openai.Config{}

			active := applyThinkConfigs(tt.model, nil, nil, &ccfg, false)

			require.False(t, active)
			if tt.effort == "" {
				require.NotContains(t, ccfg.ExtraParams, "reasoning_effort")
				require.Empty(t, ccfg.ReasoningEffort)
			} else {
				require.Equal(t, tt.effort, ccfg.ExtraParams["reasoning_effort"])
			}
		})
	}

	t.Run("omitting effort preserves other extra params", func(t *testing.T) {
		configuredExtraParams := map[string]any{
			"reasoning_effort": "high",
			"custom":           true,
		}
		ccfg := openai.Config{ExtraParams: configuredExtraParams}

		active := applyThinkConfigs(Model{API: "openai", Name: "gpt-4o"}, nil, nil, &ccfg, false)

		require.False(t, active)
		require.NotContains(t, ccfg.ExtraParams, "reasoning_effort")
		require.Equal(t, true, ccfg.ExtraParams["custom"])
		require.Equal(t, "high", configuredExtraParams["reasoning_effort"])
	})

	t.Run("Google off resets existing budget to zero", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 8192}

		active := applyThinkConfigs(Model{API: "google", ThinkingType: "enabled"}, &gccfg, nil, &openai.Config{}, false)

		require.False(t, active)
		require.Equal(t, 0, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	t.Run("Ollama skips thinking entirely", func(t *testing.T) {
		ccfg := openai.Config{ExtraParams: map[string]any{"thinking": map[string]any{"type": "disabled"}}}

		active := applyThinkConfigs(Model{API: "ollama", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		require.False(t, active)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})
}
