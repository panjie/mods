package app

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/mods/internal/proto"
	"github.com/charmbracelet/mods/internal/stream"
)

func (m *Mods) classifyShellCommand(tool, command string) bool {
	cacheKey := tool + "\x00" + command
	if cached, ok := shellClassifyCache.Load(cacheKey); ok {
		needsReview := cached.(bool)
		debug.Printf("classifyShellCommand: cmd=%q cached -> needsReview=%v", debug.Truncate(command, 80), needsReview)
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
	debug.Printf("classifyShellCommand: using model=%s api=%s, system=%q", mod.Name, mod.API, system)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: fmt.Sprintf("Tool: %s\nCommand:\n%s", tool, command)},
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
	needsReview := classifyResponse(rawResponse)
	debug.Printf("classifyShellCommand: cmd=%q resp=%s -> needsReview=%v",
		command, debug.Truncate(rawResponse, 80), needsReview)

	shellClassifyCache.Store(cacheKey, needsReview)
	return needsReview
}

func classifyResponse(raw string) bool {
	upper := strings.ToUpper(raw)
	hasYes := reYes.MatchString(upper)
	hasNo := reNo.MatchString(upper)
	return !hasNo || hasYes
}

var reYes = regexp.MustCompile(`\bYES\b`)
var reNo = regexp.MustCompile(`\bNO\b`)

var shellClassifyCache sync.Map
