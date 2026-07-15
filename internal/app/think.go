package app

import (
	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
)

const defaultThinkingBudget = 8192

func (m *Mods) resolveThink(
	mod *Model,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
) (bool, error) {
	active := applyThinkConfigs(*mod, gccfg, accfg, ccfg, m.Config.Think)
	if m.Config.Think && active {
		debug.Printf("Think: enabled for %s/%s", mod.API, mod.Name)
	} else if m.Config.Think {
		debug.Printf("Think: requested for %s/%s but thinking-type is not configured; keeping thinking off", mod.API, mod.Name)
	}
	return active, nil
}

// applyThinkConfigs applies the unified thinking policy and returns whether
// thinking is actually active. thinking-type is the opt-in switch for user
// visible thinking: when it is absent, even a requested -t keeps thinking off.
func applyThinkConfigs(mod Model, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config, requested bool) bool {
	enabled := requested && mod.ThinkingType != ""
	switch mod.API {
	case "google":
		if enabled {
			gccfg.ThinkingBudget = resolvedThinkingBudget(mod, gccfg.ThinkingBudget)
			gccfg.ThinkingBudgetExplicit = true
			debug.Printf("Think: google thinking_budget=%d", gccfg.ThinkingBudget)
			return true
		}
		// Gemini defaults to thinking enabled; explicitly send budget=0 to
		// keep discovered models non-thinking unless thinking-type opts in.
		gccfg.ThinkingBudget = 0
		gccfg.ThinkingBudgetExplicit = true
		debug.Printf("Think: google thinking_budget=0 (thinking off)")
		return false
	case "anthropic":
		if enabled {
			accfg.ThinkingBudget = resolvedThinkingBudget(mod, accfg.ThinkingBudget)
			debug.Printf("Think: anthropic thinking_budget=%d", accfg.ThinkingBudget)
			return true
		}
		// Anthropic defaults to thinking off; no request field is needed.
		return false
	case "ollama":
		debug.Printf("Think: %s does not support thinking, skipped", mod.API)
		return false
	default:
		return applyOpenAICompatibleThinking(mod, ccfg, enabled)
	}
}

func applyOpenAICompatibleThinking(mod Model, ccfg *openai.Config, enabled bool) bool {
	ccfg.ThinkTags = enabled
	if !enabled {
		disableOpenAICompatibleThink(mod, ccfg)
		return false
	}

	if mod.API == "qwen" || hasExtraParam(ccfg, "enable_thinking") {
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["enable_thinking"] = true
		debug.Printf("Think: enable_thinking=true")
		return true
	}

	if useReasoningEffort(mod, ccfg) {
		effort := mod.ReasoningEffort
		if effort == "" {
			effort = string(openai.ReasoningEffortMedium)
		}
		if hasExtraParam(ccfg, "reasoning_effort") {
			ccfg.ExtraParams["reasoning_effort"] = effort
			debug.Printf("Think: extra-params.reasoning_effort=%s", effort)
			return true
		}
		ccfg.ReasoningEffort = shared.ReasoningEffort(effort)
		debug.Printf("Think: reasoning_effort=%s", effort)
		return true
	}

	thinking := ensureThinkingParam(ccfg)
	thinking["type"] = mod.ThinkingType
	// MiniMax's adaptive mode rejects budget_tokens. Other thinking.type values
	// keep provider-specific nested fields intact.
	if mod.ThinkingType == "adaptive" {
		delete(thinking, "budget_tokens")
	}
	debug.Printf("Think: thinking.type=%s", mod.ThinkingType)
	return true
}

// disableOpenAICompatibleThink sends the provider-appropriate off signal so
// models discovered into config stay non-thinking by default.
func disableOpenAICompatibleThink(mod Model, ccfg *openai.Config) {
	if mod.API == "qwen" || hasExtraParam(ccfg, "enable_thinking") {
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["enable_thinking"] = false
		debug.Printf("Think: enable_thinking=false (thinking off)")
		return
	}

	if useReasoningEffort(mod, ccfg) {
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["reasoning_effort"] = "minimal"
		debug.Printf("Think: reasoning_effort=minimal (thinking off, cannot fully disable for %s)", mod.API)
		return
	}

	if !usesThinkingType(mod, ccfg) {
		debug.Printf("Think: no thinking off parameter for %s/%s", mod.API, mod.Name)
		return
	}

	thinking := ensureThinkingParam(ccfg)
	thinking["type"] = "disabled"
	debug.Printf("Think: thinking.type=disabled (thinking off)")
}

func resolvedThinkingBudget(mod Model, current int) int {
	if current > 0 {
		return current
	}
	if mod.ThinkingBudget > 0 {
		return mod.ThinkingBudget
	}
	return defaultThinkingBudget
}

func ensureExtraParams(ccfg *openai.Config) {
	if ccfg.ExtraParams == nil {
		ccfg.ExtraParams = map[string]any{}
	}
}

func hasExtraParam(ccfg *openai.Config, key string) bool {
	if ccfg == nil || ccfg.ExtraParams == nil {
		return false
	}
	_, ok := ccfg.ExtraParams[key]
	return ok
}

func ensureThinkingParam(ccfg *openai.Config) map[string]any {
	ensureExtraParams(ccfg)
	thinking, _ := ccfg.ExtraParams["thinking"].(map[string]any)
	if thinking == nil {
		thinking = map[string]any{}
		ccfg.ExtraParams["thinking"] = thinking
	}
	return thinking
}

func useReasoningEffort(mod Model, ccfg *openai.Config) bool {
	if mod.API == "openai" || mod.API == "azure" || mod.API == "azure-ad" {
		return true
	}
	if hasExtraParam(ccfg, "reasoning_effort") {
		return true
	}
	return mod.ReasoningEffort != ""
}

func usesThinkingType(mod Model, ccfg *openai.Config) bool {
	switch mod.API {
	case "deepseek", "glm", "minimax":
		return true
	}
	if mod.ThinkingType != "" {
		return true
	}
	_, ok := ccfg.ExtraParams["thinking"].(map[string]any)
	return ok
}
