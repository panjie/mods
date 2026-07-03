package app

import (
	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
)

func (m *Mods) resolveReasoning(
	mod *Model,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
) (bool, error) {
	cfg := m.Config
	switch cfg.Reasoning {
	case ReasoningOn:
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg, true)
		debug.Printf("Reasoning: enabled for %s/%s", mod.API, mod.Name)
		return true, nil
	case ReasoningOff:
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg, false)
		return false, nil
	default:
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg, false)
		return false, nil
	}
}

func applyReasoningConfigs(mod Model, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config, enabled bool) {
	switch {
	case mod.API == "google":
		if enabled {
			if gccfg.ThinkingBudget == 0 {
				gccfg.ThinkingBudget = 8192
			}
			debug.Printf("Reasoning: google thinking_budget=%d", gccfg.ThinkingBudget)
		} else {
			// Gemini defaults to thinking ENABLED; explicitly send budget=0
			// to turn it off and save tokens.
			gccfg.ThinkingBudget = 0
			gccfg.ThinkingBudgetExplicit = true
			debug.Printf("Reasoning: google thinking_budget=0 (-r off)")
		}
	case mod.API == "anthropic":
		if enabled {
			accfg.ThinkingBudget = 8192
			debug.Printf("Reasoning: anthropic thinking_budget=%d", accfg.ThinkingBudget)
		}
		// off: Anthropic defaults to thinking OFF, so no-op.
	case mod.API == "cohere" || mod.API == "ollama":
		debug.Printf("Reasoning: %s does not support reasoning, skipped", mod.API)
	default:
		// OpenAI-compatible providers (e.g. MiniMax, GLM) may inline their
		// reasoning inside <think>...</think> blocks in the content stream;
		// enable tag parsing only when reasoning is on so it gets separated
		// from the answer.
		ccfg.ThinkTags = enabled

		if !enabled {
			disableOpenAICompatibleReasoning(mod, ccfg)
			return
		}

		thinkingType := mod.ThinkingType

		// Determine whether this provider uses thinking.type for reasoning
		// control, and resolve the "on" value.
		thinking, hasThinking := ccfg.ExtraParams["thinking"].(map[string]any)
		if !hasThinking && thinkingType != "" {
			// User explicitly set thinking-type but has no thinking block in
			// extra-params; auto-create one so -r turns thinking on without
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
			debug.Printf("Reasoning: thinking.type=%s", thinkingType)
			return
		}

		// Fall back to OpenAI-style reasoning_effort.
		effort := mod.ReasoningEffort
		if effort == "" {
			effort = string(openai.ReasoningEffortMedium)
		}
		if _, ok := ccfg.ExtraParams["reasoning_effort"]; ok {
			ccfg.ExtraParams["reasoning_effort"] = effort
			debug.Printf("Reasoning: extra-params.reasoning_effort=%s", effort)
			return
		}
		ccfg.ReasoningEffort = shared.ReasoningEffort(effort)
		debug.Printf("Reasoning: reasoning_effort=%s", effort)
	}
}

// disableOpenAICompatibleReasoning sends the provider-appropriate "off"
// signal so that thinking-enabled-by-default providers (DeepSeek, GLM, Kimi,
// MiniMax, Qwen) do not silently consume reasoning tokens when -r is off.
// The mechanism is auto-detected from the model's configured reasoning style.
func disableOpenAICompatibleReasoning(mod Model, ccfg *openai.Config) {
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
		debug.Printf("Reasoning: thinking.type=disabled (-r off)")
		return
	}

	// 2. enable_thinking style (Qwen DashScope). The config carries
	//    enable_thinking: true as both the toggle marker and the on-value;
	//    flip to false here.
	if _, has := ccfg.ExtraParams["enable_thinking"]; has {
		ccfg.ExtraParams["enable_thinking"] = false
		debug.Printf("Reasoning: enable_thinking=false (-r off)")
		return
	}

	// 3. reasoning_effort style (OpenAI gpt-5.x, o-series). These cannot be
	//    cleanly disabled — send the lowest effort as the closest equivalent.
	if mod.API == "openai" || mod.API == "azure" {
		ccfg.ExtraParams["reasoning_effort"] = "minimal"
		debug.Printf("Reasoning: reasoning_effort=minimal (-r off, cannot fully disable for %s)", mod.API)
		return
	}

	debug.Printf("Reasoning: no built-in disable for %s/%s (-r off); relying on extra-params or provider default", mod.API, mod.Name)
}
