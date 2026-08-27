package app

import (
	"container/list"
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

const defaultPromptIntentPrompt = prompts.PromptIntentClassifier

// promptIntentCacheCapacity bounds the in-memory cache of prompt-intent
// classifier results across turns within one process.
const promptIntentCacheCapacity = 64

// promptIntentCache memoizes classifier output keyed by system prompt and
// user message so a repeated prompt (or a retried request) costs no extra
// LLM round-trip. The nil-ability of the stored value is irrelevant: an
// empty intent slice is stored and reused like any other fail-closed result.
var promptIntentCache = newPromptIntentLRU(promptIntentCacheCapacity)

// classifyPromptIntent maps the user's message onto the closed prompt-intent
// enumeration with one LLM call. Any failure (timeout, stream error, parse
// error, empty message) returns nil, which disables the prompt-intent gate
// for the turn: authorization then falls back to the standard matrix.
func (m *Mods) classifyPromptIntent(content string) []approval.PromptIntent {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if m.promptIntentAnalyzer != nil {
		return m.promptIntentAnalyzer(content)
	}
	system, err := m.resolvePrompt(prompts.KeyPromptIntentClassifier, defaultPromptIntentPrompt)
	if err != nil {
		debug.Printf("classifyPromptIntent: prompt override failed: %v", err)
		return nil
	}
	cacheKey := strings.Join([]string{system, content}, "\x00")
	if cached, ok := promptIntentCache.Load(cacheKey); ok {
		return cached
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return nil
	}
	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return nil
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI
	applyThinkConfigsWithOllama(mod, &gccfg, &accfg, &occfg, &ccfg, false)

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	debug.Printf("classifyPromptIntent: using model=%s api=%s", mod.Name, mod.API)
	maxTokens := int64(128)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: content},
		},
		API:         mod.API,
		Model:       mod.Name,
		Temperature: ptrOrNil(float64(0)),
		MaxTokens:   &maxTokens,
	}

	client, err := newStreamClient(modelProtocol(mod), accfg, gccfg, occfg, ccfg)
	if err != nil {
		return nil
	}

	st := client.Request(classifyCtx, request)
	defer func() { _ = st.Close() }()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			promptIntentCache.Store(cacheKey, nil)
			return nil
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		promptIntentCache.Store(cacheKey, nil)
		return nil
	}
	rawResponse := strings.TrimSpace(sb.String())
	intents := parsePromptIntentClassifierResponse(rawResponse)
	debug.Printf("classifyPromptIntent: resp=%s -> intents=%v", debug.Truncate(rawResponse, 80), intents)

	promptIntentCache.Store(cacheKey, intents)
	return intents
}

// parsePromptIntentClassifierResponse accepts raw, fenced, or embedded JSON
// and drops unknown labels; malformed replies yield no intents.
func parsePromptIntentClassifierResponse(raw string) []approval.PromptIntent {
	if intents := approval.ParsePromptIntentResponse(raw); intents != nil {
		return intents
	}
	for _, fenced := range extractFencedJSON(raw) {
		if intents := approval.ParsePromptIntentResponse(fenced); intents != nil {
			return intents
		}
	}
	for _, candidate := range extractJSONObjectCandidates(raw) {
		if intents := approval.ParsePromptIntentResponse(candidate); intents != nil {
			return intents
		}
	}
	return nil
}

// confirmWorkspaceWrite asks the shell classifier whether a write command
// whose target could not be derived statically stays inside the workspace.
// It returns true only when the classifier reports a concrete write effect
// whose every affected directory is the workspace; empty, external, or
// unknown results fail closed.
func (m *Mods) confirmWorkspaceWrite(tool, command string) bool {
	if m.workspaceWriteConfirmer != nil {
		return m.workspaceWriteConfirmer(tool, command)
	}
	completion := m.classifyShellWithLLM(tool, command)
	workspace, _ := m.shellClassifierPathContext()
	return workspaceWriteConfirmed(completion, workspace, m.safeDirs())
}

// workspaceWriteConfirmed reports whether a classifier completion positively
// proves a workspace-scoped write: a concrete write effect whose every
// affected directory is the workspace. Empty dirs and external dirs fail
// closed.
func workspaceWriteConfirmed(completion approval.CommandAssessment, workspace string, safeDirs []string) bool {
	if completion.Effect != approval.EffectWrite || len(completion.KnownDirs) == 0 {
		return false
	}
	if strings.TrimSpace(workspace) == "" {
		return false
	}
	for _, dir := range completion.KnownDirs {
		if pathutil.Location(dir, workspace, safeDirs) != pathutil.LocationWorkspace {
			return false
		}
	}
	return true
}

// promptIntentGrants evaluates the prompt-intent authorization gate and
// returns the labels that covered the call, or "" when the gate does not
// apply. IntentWorkspaceEdit covers write groups whose every directory is
// the workspace; IntentGlobalRead covers read groups anywhere (including
// runtime-resolved targets). External writes, unknown effects, and dynamic
// write targets are never covered.
func promptIntentGrants(deps reviewerDeps, intent AccessIntent, assessment approval.CommandAssessment, scope Scope, safeDirs []string) string {
	if len(deps.promptIntents) == 0 {
		return ""
	}
	if deps.shellExecution && assessment.Effect == approval.EffectUnknown {
		return ""
	}
	if intent.HasUnresolvedPaths() && intent.DominantClass() == AccessWrite {
		return ""
	}
	hasGlobalRead := slices.Contains(deps.promptIntents, approval.IntentGlobalRead)
	hasWorkspaceEdit := slices.Contains(deps.promptIntents, approval.IntentWorkspaceEdit)
	groups := intent.Groups()
	if len(groups) == 0 {
		return ""
	}
	coveredGlobalRead := false
	coveredWorkspaceEdit := false
	for _, group := range groups {
		switch group.Class {
		case AccessRead:
			if !hasGlobalRead {
				return ""
			}
			coveredGlobalRead = true
		case AccessWrite:
			if !hasWorkspaceEdit {
				return ""
			}
			if len(group.Dirs) == 0 {
				return ""
			}
			for _, dir := range group.Dirs {
				if pathutil.Location(dir, scope.Value, safeDirs) != pathutil.LocationWorkspace {
					return ""
				}
			}
			coveredWorkspaceEdit = true
		default:
			return ""
		}
	}
	var labels []string
	if coveredGlobalRead {
		labels = append(labels, string(approval.IntentGlobalRead))
	}
	if coveredWorkspaceEdit {
		labels = append(labels, string(approval.IntentWorkspaceEdit))
	}
	if len(labels) == 0 {
		return ""
	}
	return strings.Join(labels, ",")
}

// promptIntentLRU is a small bounded LRU mapping cache keys to intent
// slices. It mirrors shellClassifyLRU so classifier output survives across
// turns without unbounded growth.
type promptIntentLRU struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List // front = most recently used
}

type promptIntentEntry struct {
	key   string
	value []approval.PromptIntent
}

func newPromptIntentLRU(capacity int) *promptIntentLRU {
	if capacity <= 0 {
		capacity = promptIntentCacheCapacity
	}
	return &promptIntentLRU{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *promptIntentLRU) Load(key string) ([]approval.PromptIntent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*promptIntentEntry).value, true
}

func (c *promptIntentLRU) Store(key string, value []approval.PromptIntent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		elem.Value.(*promptIntentEntry).value = value
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&promptIntentEntry{key: key, value: value})
	c.items[key] = elem
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*promptIntentEntry).key)
		}
	}
}
