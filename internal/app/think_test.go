package app

import (
	"testing"

	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
	"github.com/stretchr/testify/require"
)

func TestResolveThink(t *testing.T) {
	t.Run("requested but model not opted in stays inactive", func(t *testing.T) {
		m := &Mods{Config: &Config{PersistentConfig: PersistentConfig{Think: true}}}
		ccfg := openai.Config{}

		active, err := m.resolveThink(&Model{API: "deepseek", Name: "deepseek-v4-flash"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.False(t, active)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
	})

	t.Run("requested and thinking-type opts in", func(t *testing.T) {
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

		active, err := m.resolveThink(&Model{API: "openai"}, nil, nil, &ccfg)

		require.NoError(t, err)
		require.False(t, active)
		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
	})
}

func TestApplyThinkConfigsOptIn(t *testing.T) {
	t.Run("DeepSeek without thinking-type stays disabled even when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "deepseek", Name: "deepseek-v4-flash"}, nil, nil, &ccfg, true)

		require.False(t, active)
		require.False(t, ccfg.ThinkTags)
		require.Equal(t, "disabled", ccfg.ExtraParams["thinking"].(map[string]any)["type"])
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

	t.Run("MiniMax adaptive strips budget_tokens only when opted in", func(t *testing.T) {
		ccfg := openai.Config{ExtraParams: map[string]any{
			"thinking": map[string]any{"type": "disabled", "budget_tokens": 4096},
		}}

		active := applyThinkConfigs(Model{API: "minimax", ThinkingType: "adaptive"}, nil, nil, &ccfg, true)

		require.True(t, active)
		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "adaptive", thinking["type"])
		_, hasBudget := thinking["budget_tokens"]
		require.False(t, hasBudget)
	})

	t.Run("GLM with thinking-type preserves non-adaptive budget_tokens", func(t *testing.T) {
		ccfg := openai.Config{ExtraParams: map[string]any{
			"thinking": map[string]any{"type": "disabled", "budget_tokens": 4096},
		}}

		active := applyThinkConfigs(Model{API: "glm", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		require.True(t, active)
		thinking := ccfg.ExtraParams["thinking"].(map[string]any)
		require.Equal(t, "enabled", thinking["type"])
		require.Equal(t, 4096, thinking["budget_tokens"])
	})

	t.Run("Google without thinking-type sends explicit budget zero even when requested", func(t *testing.T) {
		gccfg := google.Config{ThinkingBudget: 2048}

		active := applyThinkConfigs(Model{API: "google"}, &gccfg, nil, &openai.Config{}, true)

		require.False(t, active)
		require.Equal(t, 0, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	t.Run("Google with thinking-type sends configured budget", func(t *testing.T) {
		gccfg := google.Config{}

		active := applyThinkConfigs(Model{API: "google", ThinkingType: "enabled", ThinkingBudget: 4096}, &gccfg, nil, &openai.Config{}, true)

		require.True(t, active)
		require.Equal(t, 4096, gccfg.ThinkingBudget)
		require.True(t, gccfg.ThinkingBudgetExplicit)
	})

	t.Run("Anthropic without thinking-type stays off", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(Model{API: "anthropic"}, nil, &accfg, nil, true)

		require.False(t, active)
		require.Equal(t, 0, accfg.ThinkingBudget)
	})

	t.Run("Anthropic with thinking-type sends budget", func(t *testing.T) {
		accfg := anthropic.Config{}

		active := applyThinkConfigs(Model{API: "anthropic", ThinkingType: "enabled"}, nil, &accfg, nil, true)

		require.True(t, active)
		require.Equal(t, defaultThinkingBudget, accfg.ThinkingBudget)
	})

	t.Run("Qwen without thinking-type sends enable_thinking false", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "qwen"}, nil, nil, &ccfg, true)

		require.False(t, active)
		require.Equal(t, false, ccfg.ExtraParams["enable_thinking"])
	})

	t.Run("Qwen with thinking-type sends enable_thinking true", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "qwen", ThinkingType: "enabled"}, nil, nil, &ccfg, true)

		require.True(t, active)
		require.Equal(t, true, ccfg.ExtraParams["enable_thinking"])
	})

	t.Run("OpenAI without thinking-type stays minimal even when requested", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "openai"}, nil, nil, &ccfg, true)

		require.False(t, active)
		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
		require.Empty(t, ccfg.ReasoningEffort)
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
}

func TestApplyThinkConfigsDisable(t *testing.T) {
	t.Run("OpenAI off sends reasoning_effort minimal", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "openai", ThinkingType: "enabled"}, nil, nil, &ccfg, false)

		require.False(t, active)
		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
	})

	t.Run("Azure off sends reasoning_effort minimal", func(t *testing.T) {
		ccfg := openai.Config{}

		active := applyThinkConfigs(Model{API: "azure", ThinkingType: "enabled"}, nil, nil, &ccfg, false)

		require.False(t, active)
		require.Equal(t, "minimal", ccfg.ExtraParams["reasoning_effort"])
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
