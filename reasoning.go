package main

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/mods/internal/anthropic"
	"github.com/charmbracelet/mods/internal/cohere"
	"github.com/charmbracelet/mods/internal/google"
	"github.com/charmbracelet/mods/internal/ollama"
	"github.com/charmbracelet/mods/internal/openai"
	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
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
		debugPrintf("Reasoning: enabled for %s/%s", mod.API, mod.Name)
		return true
	case ReasoningAuto:
		if content == "" {
			return false
		}
		if mod.API == "cohere" || mod.API == "ollama" {
			return false
		}
		debugPrintf("Auto judge: evaluating task complexity for model=%s", mod.Name)
		m.activeOperation = "Evaluating task complexity..."
		judgeCtx, cancel := context.WithTimeout(m.ctx, 10*time.Second)
		defer cancel()
		// Reset reasoning configs for the judge call
		gccfgJ := *gccfg
		gccfgJ.ThinkingBudget = 0
		accfgJ := *accfg
		accfgJ.ThinkingBudget = 0
		ccfgJ := *ccfg
		ccfgJ.ReasoningEffort = ""
		shouldReason := judgeTaskComplexity(judgeCtx, mod, content, accfgJ, gccfgJ, ccfgJ, occfg, cccfg)
		debugPrintf("Auto judge: reasoning=%v", shouldReason)
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
		debugPrintf("Reasoning: google thinking_budget=%d", gccfg.ThinkingBudget)
	case api == "anthropic":
		accfg.ThinkingBudget = 8192
		debugPrintf("Reasoning: anthropic thinking_budget=%d", accfg.ThinkingBudget)
	case api == "cohere" || api == "ollama":
		debugPrintf("Reasoning: %s does not support reasoning, skipped", api)
	default:
		ccfg.ReasoningEffort = openai.ReasoningEffortMedium
		debugPrintf("Reasoning: openai reasoning_effort=%s", ccfg.ReasoningEffort)
	}
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

func (m *Mods) classifyShellCommand(command string) bool {
	if isObviouslyReadOnly(command) {
		debugPrintf("classifyShellCommand: cmd=%q classified as read-only by heuristic, auto-approving", truncateStr(command, 80))
		return false
	}

	if cached, ok := shellClassifyCache.Load(command); ok {
		needsReview := cached.(bool)
		debugPrintf("classifyShellCommand: cmd=%q cached -> needsReview=%v", truncateStr(command, 80), needsReview)
		return needsReview
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return true
	}

	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return true
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	cccfg := cfgs.Cohere
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	system := m.Config.ShellClassifyPrompt
	if system == "" {
		system = "Classify this shell command. Does it create, delete, or modify any files, directories, system settings, or persistent state? Answer only YES or NO. If unsure, answer YES."
	}
	debugPrintf("classifyShellCommand: using model=%s api=%s, system=%q", mod.Name, mod.API, system)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: command},
		},
		Model:       mod.Name,
		Temperature: ptrOrNil(cfg.Temperature),
	}

	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		return true
	}

	st := client.Request(classifyCtx, request)
	defer st.Close()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return true
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return true
	}
	rawResponse := strings.TrimSpace(sb.String())
	upper := strings.ToUpper(rawResponse)
	hasYes := reYes.MatchString(upper)
	hasNo := reNo.MatchString(upper)
	needsReview := !hasNo || hasYes
	debugPrintf("classifyShellCommand: cmd=%q resp=%s hasYes=%v hasNo=%v -> needsReview=%v",
		command, truncateStr(rawResponse, 80), hasYes, hasNo, needsReview)

	shellClassifyCache.Store(command, needsReview)
	return needsReview
}

var reYes = regexp.MustCompile(`\bYES\b`)
var reNo = regexp.MustCompile(`\bNO\b`)

var shellClassifyCache sync.Map

var reReadOnlyCmd = regexp.MustCompile(`^(echo|dir|type|whoami|hostname|ver|date|time|set|path|cd|chdir|where|pwd)\b`)

var rePwshMutOp = regexp.MustCompile(`(?i)\b(Remove-Item|Delete|Set-Content|Out-File|New-Item|Copy-Item|Move-Item|Rename-Item|mkdir|rmdir|del\s|rd\s|ren\s)\b`)

var rePwshReadCmd = regexp.MustCompile(`(?i)\bpowershell\s+-`)

func hasShellRedirect(cmd string) bool {
	for i := 0; i < len(cmd); i++ {
		if cmd[i] == '>' {
			rest := strings.TrimSpace(cmd[i+1:])
			lower := strings.ToLower(rest)
			if strings.HasPrefix(lower, "nul") && (len(rest) == 3 || rest[3] == ' ' || rest[3] == '&' || rest[3] == '|') {
				continue
			}
			return true
		}
	}
	return false
}
func isObviouslyReadOnly(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	lower := strings.ToLower(trimmed)

	if reReadOnlyCmd.MatchString(lower) {
		if hasShellRedirect(trimmed) {
			return false
		}
		return true
	}

	if rePwshReadCmd.MatchString(lower) {
		if rePwshMutOp.MatchString(trimmed) || hasShellRedirect(trimmed) {
			return false
		}
		return true
	}

	return false
}
