package app

import (
	"context"
	"errors"
	"strings"
	"time"

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
		applyReasoningConfigs(mod.API, gccfg, accfg, ccfg)
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
			applyReasoningConfigs(mod.API, gccfg, accfg, ccfg)
		}
		return shouldReason
	default:
		return false
	}
}

func applyReasoningConfigs(api string, gccfg *google.Config, accfg *anthropic.Config, ccfg *openai.Config) {
	switch {
	case api == "google":
		if gccfg.ThinkingBudget == 0 {
			gccfg.ThinkingBudget = 8192
		}
		debug.Printf("Reasoning: google thinking_budget=%d", gccfg.ThinkingBudget)
	case api == "anthropic":
		accfg.ThinkingBudget = 8192
		debug.Printf("Reasoning: anthropic thinking_budget=%d", accfg.ThinkingBudget)
	case api == "cohere" || api == "ollama":
		debug.Printf("Reasoning: %s does not support reasoning, skipped", api)
	default:
		// OpenAI-compatible providers (e.g. MiniMax) inline their reasoning
		// inside <think>...</think> blocks in the content stream; enable tag
		// parsing so it gets separated from the answer for distinct styling.
		ccfg.ThinkTags = true
		// Anthropic-compatible APIs exposed via OpenAI-compatible interface
		// (e.g. MiniMax) honor `thinking.type` and ignore OpenAI's
		// `reasoning_effort`. MiniMax's allowed types are `adaptive` /
		// `disabled` (Anthropic's `enabled` is rejected), and the
		// `adaptive` schema does not take `budget_tokens`. If the user has
		// pre-set `thinking` in extra-params to disable it, flip it to
		// `adaptive` so that -T / --reasoning on actually turns thinking
		// on.
		if thinking, ok := ccfg.ExtraParams["thinking"].(map[string]any); ok {
			thinking["type"] = "adaptive"
			delete(thinking, "budget_tokens")
			ccfg.ExtraParams["thinking"] = thinking
			debug.Printf("Reasoning: anthropic-style thinking enabled via extra-params, type=adaptive")
			return
		}
		// If the user pinned `reasoning_effort` in extra-params, upgrade it
		// in-place. extra-params are applied via WithJSONSet *after* the
		// body is serialized, so they would otherwise override the body
		// field and silently win.
		if _, ok := ccfg.ExtraParams["reasoning_effort"]; ok {
			ccfg.ExtraParams["reasoning_effort"] = string(openai.ReasoningEffortMedium)
			debug.Printf("Reasoning: extra-params.reasoning_effort upgraded to medium")
			return
		}
		ccfg.ReasoningEffort = openai.ReasoningEffortMedium
		debug.Printf("Reasoning: openai reasoning_effort=%s", ccfg.ReasoningEffort)
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
