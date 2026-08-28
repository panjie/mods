package app

import (
	"container/list"
	"context"
	"errors"
	"fmt"
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

const (
	defaultPromptIntentPrompt = prompts.PromptIntentClassifier
	defaultWriteScopePrompt   = prompts.WriteScopeClassifier
)

// classifierCacheCapacity bounds the in-memory caches of the prompt-intent
// and write-scope classifiers across turns within one process.
const classifierCacheCapacity = 64

var (
	promptIntentCache = newLRUCache[[]approval.PromptIntent](classifierCacheCapacity)
	writeScopeCache   = newLRUCache[[]approval.WriteScope](classifierCacheCapacity)
)

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

// classifyWriteScope asks the write-scope classifier where a write command's
// local filesystem effect lands. A nil/empty-then-failed result becomes
// WriteScopeUnknown so the gate never authorizes what it could not positively
// scope; an empty scopes slice means "no local filesystem write" (a purely
// remote/network operation).
func (m *Mods) classifyWriteScope(tool, command string) []approval.WriteScope {
	if m.writeScopeClassifier != nil {
		return m.writeScopeClassifier(tool, command)
	}
	system, err := m.resolvePrompt(prompts.KeyWriteScopeClassifier, defaultWriteScopePrompt)
	if err != nil {
		debug.Printf("classifyWriteScope: prompt override failed: %v", err)
		return []approval.WriteScope{approval.WriteScopeUnknown}
	}
	workspace, home := m.shellClassifierPathContext()
	userMessage := fmt.Sprintf(
		"Tool: %s\nExecution context (authoritative):\nWorkspace: %s\nHome: %s\nCommand:\n%s",
		tool, classifierContextValue(workspace), classifierContextValue(home), command,
	)
	cacheKey := strings.Join([]string{tool, command, system}, "\x00")
	if cached, ok := writeScopeCache.Load(cacheKey); ok {
		return cached
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return []approval.WriteScope{approval.WriteScopeUnknown}
	}
	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return []approval.WriteScope{approval.WriteScopeUnknown}
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI
	applyThinkConfigsWithOllama(mod, &gccfg, &accfg, &occfg, &ccfg, false)

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	debug.Printf("classifyWriteScope: using model=%s api=%s", mod.Name, mod.API)
	maxTokens := int64(128)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: userMessage},
		},
		API:         mod.API,
		Model:       mod.Name,
		Temperature: ptrOrNil(float64(0)),
		MaxTokens:   &maxTokens,
	}

	client, err := newStreamClient(modelProtocol(mod), accfg, gccfg, occfg, ccfg)
	if err != nil {
		return []approval.WriteScope{approval.WriteScopeUnknown}
	}

	st := client.Request(classifyCtx, request)
	defer func() { _ = st.Close() }()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			writeScopeCache.Store(cacheKey, []approval.WriteScope{approval.WriteScopeUnknown})
			return []approval.WriteScope{approval.WriteScopeUnknown}
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		writeScopeCache.Store(cacheKey, []approval.WriteScope{approval.WriteScopeUnknown})
		return []approval.WriteScope{approval.WriteScopeUnknown}
	}
	rawResponse := strings.TrimSpace(sb.String())
	scopes := parseWriteScopeClassifierResponse(rawResponse)
	if scopes == nil {
		scopes = []approval.WriteScope{approval.WriteScopeUnknown}
	}
	debug.Printf("classifyWriteScope: resp=%s -> scopes=%v", debug.Truncate(rawResponse, 80), scopes)

	writeScopeCache.Store(cacheKey, scopes)
	return scopes
}

// parseWriteScopeClassifierResponse accepts raw, fenced, or embedded JSON.
// A nil result signals malformed output or an unrecognized label and is
// mapped to the fail-closed WriteScopeUnknown by the caller.
func parseWriteScopeClassifierResponse(raw string) []approval.WriteScope {
	if scopes := approval.ParseWriteScopeResponse(raw); scopes != nil {
		return scopes
	}
	for _, fenced := range extractFencedJSON(raw) {
		if scopes := approval.ParseWriteScopeResponse(fenced); scopes != nil {
			return scopes
		}
	}
	for _, candidate := range extractJSONObjectCandidates(raw) {
		if scopes := approval.ParseWriteScopeResponse(candidate); scopes != nil {
			return scopes
		}
	}
	return nil
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
	coveredRemote := false
	for _, group := range groups {
		switch group.Class {
		case AccessRead:
			if !hasGlobalRead {
				return ""
			}
			coveredGlobalRead = true
		case AccessWrite:
			if len(group.Dirs) > 0 {
				if !hasWorkspaceEdit {
					return ""
				}
				for _, dir := range group.Dirs {
					if pathutil.Location(dir, scope.Value, safeDirs) != pathutil.LocationWorkspace {
						return ""
					}
				}
				coveredWorkspaceEdit = true
				continue
			}
			// No static target: rely on the write-scope classifier. nil means
			// the classifier never ran (feature off / not a shell tool), so
			// fail closed.
			if deps.writeScopes == nil {
				return ""
			}
			for _, s := range deps.writeScopes {
				switch s {
				case approval.WriteScopeExternal, approval.WriteScopeUnknown:
					return ""
				case approval.WriteScopeWorkspace:
					if !hasWorkspaceEdit {
						return ""
					}
					coveredWorkspaceEdit = true
				}
			}
			if len(deps.writeScopes) == 0 {
				// No local filesystem write: a purely remote/network operation
				// is not gated.
				coveredRemote = true
			}
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
	if coveredRemote {
		labels = append(labels, "remote")
	}
	if len(labels) == 0 {
		return ""
	}
	return strings.Join(labels, ",")
}

// lruCache is a small bounded LRU mapping cache keys to values. It mirrors
// shellClassifyLRU so classifier output survives across turns without
// unbounded growth.
type lruCache[V any] struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List // front = most recently used
}

type lruEntry[V any] struct {
	key   string
	value V
}

func newLRUCache[V any](capacity int) *lruCache[V] {
	if capacity <= 0 {
		capacity = classifierCacheCapacity
	}
	return &lruCache[V]{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *lruCache[V]) Load(key string) (V, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var zero V
	elem, ok := c.items[key]
	if !ok {
		return zero, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*lruEntry[V]).value, true
}

func (c *lruCache[V]) Store(key string, value V) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		elem.Value.(*lruEntry[V]).value = value
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&lruEntry[V]{key: key, value: value})
	c.items[key] = elem
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry[V]).key)
		}
	}
}
