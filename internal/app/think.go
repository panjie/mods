package app

import (
	"regexp"
	"strings"

	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
)

const defaultThinkingBudget = 8192

var (
	gpt5OriginalReasoningModelRe  = regexp.MustCompile(`^gpt-5(?:-(?:mini|nano))?(?:-\d{4}-\d{2}-\d{2})?$`)
	gpt5VersionedReasoningModelRe = regexp.MustCompile(`^gpt-5\.[1-9][0-9]*(?:-|$)`)
	anthropicAlwaysAdaptiveRe     = regexp.MustCompile(`^claude-(?:(?:fable|mythos)-5|mythos-preview)(?:-|$)`)
	anthropicAdaptiveRe           = regexp.MustCompile(`^claude-(?:opus-4-(?:6|7|8)|sonnet-(?:4-6|5))(?:-|$)`)
	anthropicManualThinkingRe     = regexp.MustCompile(`^claude-(?:sonnet-3-7|(?:sonnet|opus)-4(?:-(?:1|5))?|haiku-4-5)(?:-\d{8})?$`)
	anthropicSonnet5Re            = regexp.MustCompile(`^claude-sonnet-5(?:-|$)`)
)

func (m *Mods) resolveThink(
	mod *Model,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
) (bool, error) {
	return m.resolveThinkWithOllama(mod, accfg, gccfg, nil, ccfg)
}

func (m *Mods) resolveThinkWithOllama(
	mod *Model,
	accfg *anthropic.Config,
	gccfg *google.Config,
	occfg *ollama.Config,
	ccfg *openai.Config,
) (bool, error) {
	if err := validateThinkingConfig(*mod, ccfg, m.Config.Think); err != nil {
		return false, err
	}
	active := applyThinkConfigsWithOllama(*mod, gccfg, accfg, occfg, ccfg, m.Config.Think)
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
	return applyThinkConfigsWithOllama(mod, gccfg, accfg, nil, ccfg, requested)
}

type thinkingPlan struct {
	Active    bool
	Mechanism string
	Effort    string
	Budget    int
	Level     string
	Value     any
}

func applyThinkConfigsWithOllama(
	mod Model,
	gccfg *google.Config,
	accfg *anthropic.Config,
	occfg *ollama.Config,
	ccfg *openai.Config,
	requested bool,
) bool {
	switch modelProtocol(mod) {
	case "google":
		plan := googleThinkingPlan(mod, gccfg, requested)
		gccfg.ThinkingBudget = plan.Budget
		gccfg.ThinkingLevel = plan.Level
		gccfg.ThinkingBudgetExplicit = plan.Mechanism == "budget"
		if plan.Level != "" {
			debug.Printf("Think: google thinking_level=%s (active=%v)", plan.Level, plan.Active)
		} else if gccfg.ThinkingBudgetExplicit {
			debug.Printf("Think: google thinking_budget=%d (active=%v)", plan.Budget, plan.Active)
		} else {
			debug.Printf("Think: google thinking config omitted (active=%v)", plan.Active)
		}
		return plan.Active
	case "anthropic":
		return applyAnthropicThinking(mod, accfg, requested)
	case "ollama":
		if occfg == nil {
			return false
		}
		plan := ollamaThinkingPlan(mod, requested)
		occfg.Think = plan.Value
		debug.Printf("Think: ollama think=%v", plan.Value)
		return plan.Active
	default:
		return applyOpenAICompatibleThinking(mod, ccfg, requested)
	}
}

func googleThinkingPlan(mod Model, current *google.Config, requested bool) thinkingPlan {
	name := strings.ToLower(strings.TrimSpace(mod.Name))
	if strings.HasPrefix(name, "gemini-3") {
		if !requested {
			return thinkingPlan{Mechanism: "level", Level: "low"}
		}
		level := normalizeGoogleThinkingLevel(mod.ReasoningEffort)
		return thinkingPlan{Active: true, Mechanism: "level", Level: level}
	}
	if !requested {
		if strings.Contains(name, "2.5-pro") {
			return thinkingPlan{}
		}
		return thinkingPlan{Mechanism: "budget", Budget: 0}
	}
	budget := -1
	if current != nil && current.ThinkingBudget != 0 {
		budget = current.ThinkingBudget
	}
	if mod.ThinkingBudget != 0 {
		budget = mod.ThinkingBudget
	}
	return thinkingPlan{Active: true, Mechanism: "budget", Budget: budget}
}

func normalizeGoogleThinkingLevel(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal", "low", "medium", "high":
		return strings.ToLower(strings.TrimSpace(effort))
	case "xhigh", "max":
		return "high"
	default:
		return ""
	}
}

func ollamaThinkingPlan(mod Model, requested bool) thinkingPlan {
	if !requested {
		return thinkingPlan{Mechanism: "ollama", Value: false}
	}
	effort := strings.ToLower(strings.TrimSpace(mod.ReasoningEffort))
	switch effort {
	case "minimal":
		effort = "low"
	case "xhigh", "max":
		effort = "high"
	}
	if effort == "" {
		return thinkingPlan{Active: true, Mechanism: "ollama", Value: true}
	}
	return thinkingPlan{Active: true, Mechanism: "ollama", Effort: effort, Value: effort}
}

func validateThinkingConfig(mod Model, ccfg *openai.Config, requested bool) error {
	effort := strings.ToLower(strings.TrimSpace(mod.ReasoningEffort))
	if modelProtocol(mod) == "openai" && string(modelProviderProfile(mod)) == "deepseek" {
		if ccfg != nil && ccfg.UseResponses && !requested {
			effort = strings.ToLower(strings.TrimSpace(mod.ReasoningEffortOff))
			if effort == "" {
				return nil
			}
			if effort != "none" && effort != "low" {
				return newUserErrorf("reasoning-effort-off %q is invalid for DeepSeek Responses; expected none or low", mod.ReasoningEffortOff)
			}
			return nil
		}
		if !requested || effort == "" {
			return nil
		}
		switch effort {
		case "low", "high", "max":
			return nil
		default:
			return newUserErrorf("reasoning-effort %q is invalid for DeepSeek; expected low, high, or max", mod.ReasoningEffort)
		}
	}
	if !requested || effort == "" {
		return nil
	}
	switch modelProtocol(mod) {
	case "google":
		if strings.HasPrefix(strings.ToLower(strings.TrimSpace(mod.Name)), "gemini-3") && normalizeGoogleThinkingLevel(effort) == "" {
			return newUserErrorf("reasoning-effort %q is invalid for Gemini 3; expected minimal, low, medium, or high", mod.ReasoningEffort)
		}
	case "ollama":
		normalized := ollamaThinkingPlan(mod, true).Effort
		if normalized != "" && normalized != "low" && normalized != "medium" && normalized != "high" {
			return newUserErrorf("reasoning-effort %q is invalid for Ollama; expected low, medium, or high", mod.ReasoningEffort)
		}
	}
	return nil
}

func openAIConfigProfile(cfg *openai.Config) openai.ProviderProfile {
	if cfg.ProviderProfile != "" {
		return cfg.ProviderProfile
	}
	if cfg.ResponsesProfile != "" {
		return cfg.ResponsesProfile
	}
	return openai.ProviderProfileOpenAI
}

func applyAnthropicThinking(mod Model, accfg *anthropic.Config, requested bool) bool {
	name := strings.ToLower(strings.TrimSpace(mod.Name))
	accfg.ThinkingType = ""
	accfg.ThinkingActive = false
	accfg.ReasoningEffort = ""

	if !requested {
		if anthropicSonnet5Re.MatchString(name) {
			accfg.ThinkingType = "disabled"
			debug.Printf("Think: anthropic thinking.type=disabled (thinking off for %s)", mod.Name)
		} else if anthropicAlwaysAdaptiveRe.MatchString(name) {
			debug.Printf("Think: anthropic thinking field omitted (model-managed and cannot be disabled for %s)", mod.Name)
		} else {
			debug.Printf("Think: anthropic thinking field omitted (thinking off)")
		}
		return false
	}

	if mod.ThinkingType != "" {
		accfg.ThinkingType = strings.ToLower(strings.TrimSpace(mod.ThinkingType))
		switch accfg.ThinkingType {
		case "enabled":
			accfg.ThinkingBudget = resolvedThinkingBudget(mod, accfg.ThinkingBudget)
			accfg.ThinkingActive = true
			debug.Printf("Think: anthropic thinking.type=enabled, budget_tokens=%d (explicit)", accfg.ThinkingBudget)
			return true
		case "adaptive":
			accfg.ThinkingActive = true
			accfg.ReasoningEffort = mod.ReasoningEffort
			debugAnthropicEffort(accfg.ReasoningEffort)
			debug.Printf("Think: anthropic thinking.type=adaptive (explicit)")
			return true
		case "disabled":
			debug.Printf("Think: anthropic thinking.type=disabled (explicit)")
			return false
		default:
			// Preserve the explicit value so the Anthropic adapter can return a
			// clear configuration error before making an HTTP request.
			debug.Printf("Think: anthropic thinking.type=%s (explicit)", accfg.ThinkingType)
			return false
		}
	}

	switch {
	case anthropicAlwaysAdaptiveRe.MatchString(name):
		accfg.ThinkingActive = true
		accfg.ReasoningEffort = mod.ReasoningEffort
		debug.Printf("Think: anthropic thinking field omitted (model-managed adaptive for %s)", mod.Name)
		debugAnthropicEffort(accfg.ReasoningEffort)
		return true
	case anthropicAdaptiveRe.MatchString(name):
		accfg.ThinkingType = "adaptive"
		accfg.ThinkingActive = true
		accfg.ReasoningEffort = mod.ReasoningEffort
		debug.Printf("Think: anthropic thinking.type=adaptive (automatic for %s)", mod.Name)
		debugAnthropicEffort(accfg.ReasoningEffort)
		return true
	case anthropicManualThinkingRe.MatchString(name):
		accfg.ThinkingType = "enabled"
		accfg.ThinkingBudget = resolvedThinkingBudget(mod, accfg.ThinkingBudget)
		accfg.ThinkingActive = true
		debug.Printf(
			"Think: anthropic thinking.type=enabled, budget_tokens=%d (automatic for %s)",
			accfg.ThinkingBudget,
			mod.Name,
		)
		return true
	default:
		debug.Printf("Think: anthropic thinking field omitted (capability unknown for %s)", mod.Name)
		return false
	}
}

func debugAnthropicEffort(effort string) {
	if effort == "" {
		debug.Printf("Think: anthropic output_config.effort omitted (provider default)")
		return
	}
	debug.Printf("Think: anthropic output_config.effort=%s", effort)
}

func applyOpenAICompatibleThinking(mod Model, ccfg *openai.Config, requested bool) bool {
	profile := string(modelProviderProfile(mod))
	if ccfg.UseResponses && openAIConfigProfile(ccfg) == openai.ProviderProfileDeepSeek {
		ccfg.ThinkTags = requested
		if !requested {
			effort := strings.ToLower(strings.TrimSpace(mod.ReasoningEffortOff))
			if effort == "" {
				effort = "none"
			}
			ensureExtraParams(ccfg)
			ccfg.ExtraParams["reasoning_effort"] = effort
			debug.Printf("Think: reasoning.effort=%s (thinking off for DeepSeek Responses)", effort)
			return false
		}
		effort := strings.ToLower(strings.TrimSpace(mod.ReasoningEffort))
		if effort == "" {
			effort = "high"
		}
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["reasoning_effort"] = effort
		debug.Printf("Think: reasoning.effort=%s (DeepSeek Responses)", effort)
		return true
	}
	if profile == "deepseek" {
		ccfg.ThinkTags = requested
		thinking := ensureThinkingParam(ccfg)
		if !requested {
			thinking["type"] = "disabled"
			omitExtraParam(ccfg, "reasoning_effort")
			debug.Printf("Think: DeepSeek Chat thinking.type=disabled")
			return false
		}
		thinking["type"] = "enabled"
		effort := strings.ToLower(strings.TrimSpace(mod.ReasoningEffort))
		if effort == "" {
			effort = "high"
		}
		ccfg.ExtraParams["reasoning_effort"] = effort
		debug.Printf("Think: DeepSeek Chat thinking.type=enabled, reasoning_effort=%s", effort)
		return true
	}
	if !requested {
		// MiniMax reasoning models can still return <think>...</think> in the
		// content stream when their undocumented thinking-off parameter is
		// ignored. Keep parsing enabled so -t controls display only; parsed
		// thought is discarded by the app when thinking is inactive.
		ccfg.ThinkTags = profile == "minimax"
		disableOpenAICompatibleThink(mod, ccfg)
		return false
	}

	if profile == "qwen" || hasExtraParam(ccfg, "enable_thinking") {
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
	profile := string(modelProviderProfile(mod))
	if profile == "qwen" || hasExtraParam(ccfg, "enable_thinking") {
		ensureExtraParams(ccfg)
		ccfg.ExtraParams["enable_thinking"] = false
		debug.Printf("Think: enable_thinking=false (thinking off)")
		return
	}

	// Kimi's thinking parameter only accepts type=enabled. Omitting the
	// parameter is its off signal; type=disabled is rejected by the API.
	if profile == "kimi" {
		omitExtraParam(ccfg, "thinking")
		debug.Printf("Think: thinking field omitted (thinking off for kimi)")
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
	switch string(modelProviderProfile(mod)) {
	case "deepseek", "glm", "kimi":
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
	if hasExtraParam(ccfg, "reasoning_effort") {
		return true
	}
	if mod.ReasoningEffort != "" || mod.ReasoningEffortOff != "" {
		return true
	}
	protocol := modelProtocol(mod)
	if string(modelProviderProfile(mod)) != "openai" ||
		(protocol != "openai" && protocol != "azure" && protocol != "github-copilot") {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(mod.Name))
	return gpt5OriginalReasoningModelRe.MatchString(name) ||
		gpt5VersionedReasoningModelRe.MatchString(name) || isOSeries(name)
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
	switch string(modelProviderProfile(mod)) {
	case "deepseek", "glm", "kimi", "minimax":
		return true
	}
	if mod.ThinkingType != "" {
		return true
	}
	_, ok := ccfg.ExtraParams["thinking"].(map[string]any)
	return ok
}
