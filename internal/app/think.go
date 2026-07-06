package app

import (
	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
)

func (m *Mods) resolveThink(
	mod *Model,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
) (bool, error) {
	if m.Config.Think {
		applyThinkConfigs(*mod, gccfg, accfg, ccfg, true)
		debug.Printf("Think: enabled for %s/%s", mod.API, mod.Name)
		return true, nil
	}
	applyThinkConfigs(*mod, gccfg, accfg, ccfg, false)
	return false, nil
}

func applyThinkConfigs(mod Model, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config, enabled bool) {
	switch {
	case mod.API == "google":
		if enabled {
			if gccfg.ThinkingBudget == 0 {
				gccfg.ThinkingBudget = 8192
			}
			debug.Printf("Think: google thinking_budget=%d", gccfg.ThinkingBudget)
		} else {
			// Gemini defaults to thinking ENABLED; explicitly send budget=0
			// to turn it off and save tokens.
			gccfg.ThinkingBudget = 0
			gccfg.ThinkingBudgetExplicit = true
			debug.Printf("Think: google thinking_budget=0 (-t off)")
		}
	case mod.API == "anthropic":
		if enabled {
			accfg.ThinkingBudget = 8192
			debug.Printf("Think: anthropic thinking_budget=%d", accfg.ThinkingBudget)
		}
		// off: Anthropic defaults to thinking OFF, so no-op.
	case mod.API == "ollama":
		debug.Printf("Think: %s does not support thinking, skipped", mod.API)
	default:
		// OpenAI-compatible providers (e.g. MiniMax, GLM) may inline their
		// reasoning inside <think>...</think> blocks in the content stream;
		// enable tag parsing only when thinking is on so it gets separated
		// from the answer.
		ccfg.ThinkTags = enabled

		if !enabled {
			disableOpenAICompatibleThink(mod, ccfg)
			return
		}

		thinkingType := mod.ThinkingType

		// Determine whether this provider uses thinking.type for reasoning
		// control, and resolve the "on" value.
		thinking, hasThinking := ccfg.ExtraParams["thinking"].(map[string]any)
		if !hasThinking && thinkingType != "" {
			// User explicitly set thinking-type but has no thinking block in
			// extra-params; auto-create one so -t turns thinking on without
			// requiring a redundant extra-params.thinking.type: disabled.
			thinking = map[string]any{}
			if ccfg.ExtraParams == nil {
				ccfg.ExtraParams = map[string]any{}
			}
			ccfg.ExtraParams["thinking"] = thinking
			hasThinking = true
		}

		if hasThinking {
			if thinkingType == "" {
				thinkingType = "adaptive" // backward compat for existing MiniMax configs
			}
			thinking["type"] = thinkingType
			// MiniMax's "adaptive" rejects budget_tokens; strip it. For
			// other values (e.g. GLM's "enabled"), leave budget_tokens as-is
			// — the user controls it via extra-params or thinking-budget.
			if thinkingType == "adaptive" {
				delete(thinking, "budget_tokens")
			}
			debug.Printf("Think: thinking.type=%s", thinkingType)
			return
		}

		// Fall back to OpenAI-style reasoning_effort.
		effort := mod.ReasoningEffort
		if effort == "" {
			effort = string(openai.ReasoningEffortMedium)
		}
		if _, ok := ccfg.ExtraParams["reasoning_effort"]; ok {
			ccfg.ExtraParams["reasoning_effort"] = effort
			debug.Printf("Think: extra-params.reasoning_effort=%s", effort)
			return
		}
		ccfg.ReasoningEffort = shared.ReasoningEffort(effort)
		debug.Printf("Think: reasoning_effort=%s", effort)
	}
}

// disableOpenAICompatibleThink sends the provider-appropriate "off"
// signal so that thinking-enabled-by-default providers (DeepSeek, GLM, Kimi,
// MiniMax, Qwen) do not silently consume reasoning tokens when -t is off.
// The mechanism is auto-detected from the model's configured reasoning style.
func disableOpenAICompatibleThink(mod Model, ccfg *openai.Config) {
	if ccfg.ExtraParams == nil {
		ccfg.ExtraParams = map[string]any{}
	}

	// 1. thinking.type style (DeepSeek, GLM, Kimi, MiniMax, Anthropic-compat).
	//    Triggered when the model declares thinking-type OR already has a
	//    thinking block in extra-params.
	if _, hasThinking := ccfg.ExtraParams["thinking"].(map[string]any); hasThinking || mod.ThinkingType != "" {
		thinking, _ := ccfg.ExtraParams["thinking"].(map[string]any)
		if thinking == nil {
			thinking = map[string]any{}
		}
		thinking["type"] = "disabled"
		ccfg.ExtraParams["thinking"] = thinking
		debug.Printf("Think: thinking.type=disabled (-t off)")
		return
	}

	// 2. enable_thinking style (Qwen DashScope). The config carries
	//    enable_thinking: true as both the toggle marker and the on-value;
	//    flip to false here.
	if _, has := ccfg.ExtraParams["enable_thinking"]; has {
		ccfg.ExtraParams["enable_thinking"] = false
		debug.Printf("Think: enable_thinking=false (-t off)")
		return
	}

	// 3. reasoning_effort style (OpenAI gpt-5.x, o-series). These cannot be
	//    cleanly disabled — send the lowest effort as the closest equivalent.
	if mod.API == "openai" || mod.API == "azure" {
		ccfg.ExtraParams["reasoning_effort"] = "minimal"
		debug.Printf("Think: reasoning_effort=minimal (-t off, cannot fully disable for %s)", mod.API)
		return
	}

	debug.Printf("Think: no built-in disable for %s/%s (-t off); relying on extra-params or provider default", mod.API, mod.Name)
}
