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
) bool {
	cfg := m.Config
	switch cfg.Reasoning {
	case ReasoningOn:
		applyReasoningConfigs(*mod, gccfg, accfg, ccfg)
		debug.Printf("Reasoning: enabled for %s/%s", mod.API, mod.Name)
		return true
	case ReasoningAuto:
		if content == "" {
			return false
		}
		if mod.API == "cohere" || mod.API == "ollama" {
			return false
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
		shouldReason := judgeTaskComplexity(judgeCtx, mod, content, accfgJ, gccfgJ, ccfgJ, occfg, cccfg)
		debug.Printf("Auto judge: reasoning=%v", shouldReason)
		if shouldReason {
			applyReasoningConfigs(*mod, gccfg, accfg, ccfg)
		}
		return shouldReason
	default:
		return false
	}
}

func applyReasoningConfigs(mod Model, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config) {
	switch {
	case mod.API == "google":
		if gccfg.ThinkingBudget == 0 {
			gccfg.ThinkingBudget = 8192
		}
		debug.Printf("Reasoning: google thinking_budget=%d", gccfg.ThinkingBudget)
	case mod.API == "anthropic":
		accfg.ThinkingBudget = 8192
		debug.Printf("Reasoning: anthropic thinking_budget=%d", accfg.ThinkingBudget)
	case mod.API == "cohere" || mod.API == "ollama":
		debug.Printf("Reasoning: %s does not support reasoning, skipped", mod.API)
	default:
		// OpenAI-compatible providers (e.g. MiniMax, GLM) may inline their
		// reasoning inside <think>...</think> blocks in the content stream;
		// enable tag parsing so it gets separated from the answer.
		ccfg.ThinkTags = true

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

// clearThinkingFromExtraParams removes anthropic-style `thinking` and any
// pre-set `reasoning_effort` from extra-params so the auto-judge call does
// not accidentally inherit the user's reasoning configuration.
func clearThinkingFromExtraParams(extra map[string]any) {
	if extra == nil {
		return
	}
	delete(extra, "thinking")
	delete(extra, "reasoning_effort")
}

func judgeTaskComplexity(
	ctx context.Context,
	mod *Model,
	prompt string,
	accfg anthropic.Config,
	gccfg google.Config,
	ccfg openai.Config,
	occfg ollama.Config,
	cccfg cohere.Config,
) bool {
	system := "You are a task classifier. Determine if the following task requires deep reasoning (multi-step analysis, debugging, complex logic, math, code design, or creative writing). Answer only YES."
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
	defer st.Close()

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
