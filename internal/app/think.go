package app

import (
	"regexp"
	"strings"

	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/openai"
)

const defaultThinkingBudget = 8192

var (
	gpt5OriginalReasoningModelRe  = regexp.MustCompile(`^gpt-5(?:-(?:mini|nano))?(?:-\d{4}-\d{2}-\d{2})?$`)
	gpt5VersionedReasoningModelRe = regexp.MustCompile(`^gpt-5\.[1-9][0-9]*(?:-|$)`)
)

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
		debug.Printf("Think: requested for %s/%s but no thinking configuration is known; keeping thinking off", mod.API, mod.Name)
	}
	return active, nil
}

// applyThinkConfigs applies the unified thinking policy and returns whether
// thinking is actually active. For built-in providers, -t / --think enables the
// provider's default thinking mechanism; thinking-type only overrides the
// provider default or opts custom providers into a thinking.type request.
func applyThinkConfigs(mod Model, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config, requested bool) bool {
	switch mod.API {
	case "google":
		if requested {
			gccfg.ThinkingBudget = resolvedThinkingBudget(mod, gccfg.ThinkingBudget)
			gccfg.ThinkingBudgetExplicit = true
			debug.Printf("Think: google thinking_budget=%d", gccfg.ThinkingBudget)
			return true
		}
		// Gemini defaults to thinking enabled; explicitly send budget=0 to
		// keep thinking off unless -t / --think is requested.
		gccfg.ThinkingBudget = 0
		gccfg.ThinkingBudgetExplicit = true
		debug.Printf("Think: google thinking_budget=0 (thinking off)")
		return false
	case "anthropic":
		if requested {
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
		return applyOpenAICompatibleThinking(mod, ccfg, requested)
	}
}

func applyOpenAICompatibleThinking(mod Model, ccfg *openai.Config, requested bool) bool {
	if !requested {
		ccfg.ThinkTags = false
		disableOpenAICompatibleThink(mod, ccfg)
		return false
	}

	if mod.API == "qwen" || hasExtraParam(ccfg, "enable_thinking") {
		ccfg.ThinkTags = true
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["enable_thinking"] = true
		debug.Printf("Think: enable_thinking=true")
		return true
	}

	if useReasoningEffort(mod, ccfg) {
		ccfg.ThinkTags = true
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

	thinkingType, ok := resolvedOpenAICompatibleThinkingType(mod, ccfg)
	if !ok {
		ccfg.ThinkTags = false
		debug.Printf("Think: no thinking on parameter for %s/%s", mod.API, mod.Name)
		return false
	}

	ccfg.ThinkTags = true
	thinking := ensureThinkingParam(ccfg)
	thinking["type"] = thinkingType
	// MiniMax's adaptive mode rejects budget_tokens. Other thinking.type values
	// keep provider-specific nested fields intact.
	if thinkingType == "adaptive" {
		delete(thinking, "budget_tokens")
	}
	debug.Printf("Think: thinking.type=%s", thinkingType)
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
		effort, ok := disabledReasoningEffort(mod)
		if !ok {
			omitExtraParam(ccfg, "reasoning_effort")
			ccfg.ReasoningEffort = ""
			debug.Printf("Think: reasoning_effort omitted (thinking off, no compatible value known for %s/%s)", mod.API, mod.Name)
			return
		}
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["reasoning_effort"] = effort
		debug.Printf("Think: reasoning_effort=%s (thinking off for %s/%s)", effort, mod.API, mod.Name)
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

func omitExtraParam(ccfg *openai.Config, key string) {
	if !hasExtraParam(ccfg, key) {
		return
	}
	extraParams := make(map[string]any, len(ccfg.ExtraParams)-1)
	for k, v := range ccfg.ExtraParams {
		if k != key {
			extraParams[k] = v
		}
	}
	ccfg.ExtraParams = extraParams
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

func resolvedOpenAICompatibleThinkingType(mod Model, ccfg *openai.Config) (string, bool) {
	if mod.ThinkingType != "" {
		return mod.ThinkingType, true
	}
	switch mod.API {
	case "deepseek", "glm":
		return "enabled", true
	case "minimax":
		return "adaptive", true
	}
	thinking, ok := ccfg.ExtraParams["thinking"].(map[string]any)
	if !ok {
		return "", false
	}
	if t, ok := thinking["type"].(string); ok && t != "" && t != "disabled" {
		return t, true
	}
	return "enabled", true
}

func useReasoningEffort(mod Model, ccfg *openai.Config) bool {
	if mod.API == "openai" || mod.API == "azure" || mod.API == "azure-ad" {
		return true
	}
	if hasExtraParam(ccfg, "reasoning_effort") {
		return true
	}
	return mod.ReasoningEffort != "" || mod.ReasoningEffortOff != ""
}

func disabledReasoningEffort(mod Model) (string, bool) {
	if mod.ReasoningEffortOff != "" {
		return mod.ReasoningEffortOff, true
	}

	model := strings.ToLower(strings.TrimSpace(mod.Name))
	if isProReasoningModel(model) {
		return "", false
	}
	switch {
	case gpt5VersionedReasoningModelRe.MatchString(model):
		return "none", true
	case gpt5OriginalReasoningModelRe.MatchString(model):
		return "minimal", true
	case isOSeries(model):
		return "low", true
	default:
		return "", false
	}
}

func isProReasoningModel(model string) bool {
	for part := range strings.SplitSeq(model, "-") {
		if part == "pro" {
			return true
		}
	}
	return false
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
