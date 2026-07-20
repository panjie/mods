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

	t.Run("Anthropic manual model sends budget when requested", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-sonnet-4-5-20250929"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Equal(t, "enabled", accfg.ThinkingType)
		require.Equal(t, defaultThinkingBudget, accfg.ThinkingBudget)
	})

	t.Run("Anthropic explicit enabled sends budget for opaque model", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "deployment-name", ThinkingType: "enabled"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Equal(t, "enabled", accfg.ThinkingType)
		require.Equal(t, defaultThinkingBudget, accfg.ThinkingBudget)
	})

	t.Run("Anthropic adaptive model uses adaptive and explicit effort", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-opus-4-8", ReasoningEffort: "xhigh"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Equal(t, "adaptive", accfg.ThinkingType)
		require.Equal(t, "xhigh", accfg.ReasoningEffort)
		require.Zero(t, accfg.ThinkingBudget)
	})

	t.Run("Anthropic adaptive model omits effort by default", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-sonnet-4-6"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Equal(t, "adaptive", accfg.ThinkingType)
		require.Empty(t, accfg.ReasoningEffort)
	})

	t.Run("Anthropic always-adaptive model leaves thinking model-managed", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-fable-5", ReasoningEffort: "high"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Empty(t, accfg.ThinkingType)
		require.True(t, accfg.ThinkingActive)
		require.Equal(t, "high", accfg.ReasoningEffort)
	})

	t.Run("Anthropic Sonnet 5 is explicitly disabled without -t", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-sonnet-5"},
			nil,
			&accfg,
			nil,
			false,
		)

		require.False(t, active)
		require.Equal(t, "disabled", accfg.ThinkingType)
	})

	t.Run("Anthropic always-adaptive model cannot be disabled", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-mythos-preview"},
			nil,
			&accfg,
			nil,
			false,
		)

		require.False(t, active)
		require.Empty(t, accfg.ThinkingType)
	})

	t.Run("Anthropic opaque model omits thinking unless explicitly configured", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "deployment-name"},
			nil,
			&accfg,
			nil,
			true,
		)

		require.False(t, active)
		require.Empty(t, accfg.ThinkingType)
		require.Zero(t, accfg.ThinkingBudget)
	})

	t.Run("Anthropic explicit adaptive overrides opaque model inference", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(
			Model{
				API:             "anthropic",
				Name:            "deployment-name",
				ThinkingType:    "adaptive",
				ReasoningEffort: "medium",
			},
			nil,
			&accfg,
			nil,
			true,
		)

		require.True(t, active)
		require.Equal(t, "adaptive", accfg.ThinkingType)
		require.Equal(t, "medium", accfg.ReasoningEffort)
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

func TestAnthropicThinkingModelFamilies(t *testing.T) {
	tests := []struct {
		name, model, wantType string
	}{
		{"Sonnet 3.7 snapshot", "claude-sonnet-3-7-20250219", "enabled"},
		{"original Sonnet 4 snapshot", "claude-sonnet-4-20250514", "enabled"},
		{"Opus 4.1 snapshot", "claude-opus-4-1-20250805", "enabled"},
		{"Opus 4.5 alias", "claude-opus-4-5", "enabled"},
		{"Haiku 4.5 snapshot", "claude-haiku-4-5-20251001", "enabled"},
		{"Opus 4.6", "claude-opus-4-6", "adaptive"},
		{"Opus 4.7", "claude-opus-4-7", "adaptive"},
		{"Sonnet 5", "claude-sonnet-5", "adaptive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := anthropic.Config{}
			active := applyThinkConfigs(
				Model{API: "anthropic", Name: tt.model},
				nil,
				&cfg,
				nil,
				true,
			)
			require.True(t, active)
			require.Equal(t, tt.wantType, cfg.ThinkingType)
		})
	}

	t.Run("known non-thinking Claude model stays off", func(t *testing.T) {
		cfg := anthropic.Config{}
		active := applyThinkConfigs(
			Model{API: "anthropic", Name: "claude-3-5-haiku-20241022"},
			nil,
			&cfg,
			nil,
			true,
		)
		require.False(t, active)
		require.Empty(t, cfg.ThinkingType)
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
