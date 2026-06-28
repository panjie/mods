package app

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-go/shared"
	"github.com/panjie/mods/internal/anthropic"
	"github.com/panjie/mods/internal/cohere"
	"github.com/panjie/mods/internal/google"
	"github.com/panjie/mods/internal/ollama"
	"github.com/panjie/mods/internal/openai"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

func (m *Mods) resolveReasoning(
	mod *Model,
	content string,
	accfg *anthropic.Config,
	gccfg *google.Config,
	ccfg *openai.Config,
	occfg ollama.Config,
	cccfg cohere.Config,
) (bool, error) {
	cfg := m.Config
	switch cfg.Reasoning {
	case ReasoningOn:
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg, true)
		debug.Printf("Reasoning: enabled for %s/%s", mod.API, mod.Name)
		return true, nil
	case ReasoningAuto:
		if content == "" {
			applyReasoningConfigs(*mod, gccfg, accfg, ccfg, false)
			return false, nil
		}
		if mod.API == "cohere" || mod.API == "ollama" {
			return false, nil
		}
		debug.Printf("Auto judge: evaluating task complexity for model=%s", mod.Name)
		m.setActiveOperation("Evaluating task complexity...")
		judgeCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		// Reset reasoning configs for the judge call
		gccfgJ := *gccfg
		gccfgJ.ThinkingBudget = 0
		accfgJ := *accfg
		accfgJ.ThinkingBudget = 0
		ccfgJ := *ccfg
		ccfgJ.ReasoningEffort = ""
		clearThinkingFromExtraParams(ccfgJ.ExtraParams)
		system, err := m.resolvePrompt(prompts.KeyReasoningClassifier, prompts.ReasoningClassifier)
		if err != nil {
			applyReasoningConfigs(*mod, gccfg, accfg, ccfg, false)
			return false, err
		}
		shouldReason := judgeTaskComplexity(judgeCtx, mod, system, content, accfgJ, gccfgJ, ccfgJ, occfg, cccfg)
		debug.Printf("Auto judge: reasoning=%v", shouldReason)
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg, shouldReason)
		return shouldReason, nil
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
			debug.Printf("Reasoning: google thinking_budget=0 (-T off)")
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
			// extra-params; auto-create one so -T turns thinking on without
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
// MiniMax, Qwen) do not silently consume reasoning tokens when -T is off.
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
		debug.Printf("Reasoning: thinking.type=disabled (-T off)")
		return
	}

	// 2. enable_thinking style (Qwen DashScope). The config carries
	//    enable_thinking: true as both the toggle marker and the on-value;
	//    flip to false here.
	if _, has := ccfg.ExtraParams["enable_thinking"]; has {
		ccfg.ExtraParams["enable_thinking"] = false
		debug.Printf("Reasoning: enable_thinking=false (-T off)")
		return
	}

	// 3. reasoning_effort style (OpenAI gpt-5.x, o-series). These cannot be
	//    cleanly disabled — send the lowest effort as the closest equivalent.
	if mod.API == "openai" || mod.API == "azure" {
		ccfg.ExtraParams["reasoning_effort"] = "minimal"
		debug.Printf("Reasoning: reasoning_effort=minimal (-T off, cannot fully disable for %s)", mod.API)
		return
	}

	debug.Printf("Reasoning: no built-in disable for %s/%s (-T off); relying on extra-params or provider default", mod.API, mod.Name)
}

// clearThinkingFromExtraParams removes anthropic-style `thinking`, Qwen-style
// `enable_thinking`, and any pre-set `reasoning_effort` from extra-params so
// the auto-judge call does not accidentally inherit the user's reasoning
// configuration.
func clearThinkingFromExtraParams(extra map[string]any) {
	if extra == nil {
		return
	}
	delete(extra, "thinking")
	delete(extra, "reasoning_effort")
	delete(extra, "enable_thinking")
}

func judgeTaskComplexity(
	ctx context.Context,
	mod *Model,
	system string,
	prompt string,
	accfg anthropic.Config,
	gccfg google.Config,
	ccfg openai.Config,
	occfg ollama.Config,
	cccfg cohere.Config,
) bool {
	max3 := int64(3)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: prompt},
		},
		Model:       mod.Name,
		MaxTokens:   &max3,
		Temperature: ptrOrNil(float64(0)),
	}

	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		return false
	}

	st := client.Request(ctx, request)
	defer func() { _ = st.Close() }()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return false
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return false
	}
	return strings.Contains(strings.ToUpper(strings.TrimSpace(sb.String())), "YES")
}
