package app

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

const defaultShellClassifyPrompt = `Analyze this shell command for review.
Return only strict JSON. Do not include <think> tags, Markdown fences, prose, or explanations.
Use exactly this shape:
{"needs_review":true,"affected_dirs":["/path/or/relative/dir"],"reason":"short reason"}

Set needs_review to true if the command creates, deletes, modifies, or may modify files, directories, system settings, or persistent state. If unsure, set needs_review to true.
Set affected_dirs to the directories that may be written, deleted, or modified. If none are affected or unknown, use an empty array.
Example: ls -la /path/to/project => {"needs_review":false,"affected_dirs":[],"reason":"lists directory contents only"}.`

type shellCommandAnalysis struct {
	NeedsReview  bool
	AffectedDirs []string
	Reason       string
}

func defaultShellCommandAnalysis() shellCommandAnalysis {
	return shellCommandAnalysis{NeedsReview: true}
}

func (m *Mods) classifyShellCommand(tool, command string) bool {
	return m.analyzeShellCommand(tool, command).NeedsReview
}

func (m *Mods) analyzeShellCommand(tool, command string) shellCommandAnalysis {
	if m.shellAnalyzer != nil {
		return m.shellAnalyzer(tool, command)
	}

	customPrompt := m.Config.ShellClassifyPrompt != ""
	cacheKey := tool + "\x00" + command + "\x00" + m.Config.ShellClassifyPrompt
	if cached, ok := shellClassifyCache.Load(cacheKey); ok {
		debug.Printf("analyzeShellCommand: cmd=%q cached -> needsReview=%v dirs=%v", debug.Truncate(command, 80), cached.NeedsReview, cached.AffectedDirs)
		return cached
	}

	if !isObviouslyMutable(command) {
		analysis := shellCommandAnalysis{
			NeedsReview:  false,
			AffectedDirs: []string{},
			Reason:       "read-only command (local heuristic)",
		}
		shellClassifyCache.Store(cacheKey, analysis)
		debug.Printf("analyzeShellCommand: cmd=%q -> local heuristic: read-only", debug.Truncate(command, 80))
		return analysis
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return defaultShellCommandAnalysis()
	}

	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return defaultShellCommandAnalysis()
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	cccfg := cfgs.Cohere
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI
	applyReasoningConfigs(mod, &gccfg, &accfg, &ccfg, false)

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	system := m.Config.ShellClassifyPrompt
	if system == "" {
		system = defaultShellClassifyPrompt
	}
	debug.Printf("analyzeShellCommand: using model=%s api=%s, structured=%v, system=%q", mod.Name, mod.API, !customPrompt, system)
	maxTokens := int64(256)
	request := proto.Request{
		Messages: []proto.Message{
			{Role: proto.RoleSystem, Content: system},
			{Role: proto.RoleUser, Content: fmt.Sprintf("Tool: %s\nCommand:\n%s", tool, command)},
		},
		API:         mod.API,
		Model:       mod.Name,
		Temperature: ptrOrNil(float64(0)),
		MaxTokens:   &maxTokens,
	}

	client, err := newStreamClient(mod.API, accfg, gccfg, cccfg, occfg, ccfg)
	if err != nil {
		return defaultShellCommandAnalysis()
	}

	st := client.Request(classifyCtx, request)
	defer func() { _ = st.Close() }()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return defaultShellCommandAnalysis()
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return defaultShellCommandAnalysis()
	}
	rawResponse := strings.TrimSpace(sb.String())
	var analysis shellCommandAnalysis
	if customPrompt {
		analysis = shellCommandAnalysis{NeedsReview: classifyResponse(rawResponse)}
	} else {
		var ok bool
		analysis, ok = parseShellAnalysisResponse(rawResponse)
		if !ok {
			analysis = defaultShellCommandAnalysis()
		}
	}
	debug.Printf("analyzeShellCommand: cmd=%q resp=%s -> needsReview=%v dirs=%v reason=%q",
		command, debug.Truncate(rawResponse, 80), analysis.NeedsReview, analysis.AffectedDirs, analysis.Reason)

	shellClassifyCache.Store(cacheKey, analysis)
	return analysis
}

func parseShellAnalysisResponse(raw string) (shellCommandAnalysis, bool) {
	if analysis, ok := parseShellAnalysisJSON(strings.TrimSpace(raw)); ok {
		return analysis, true
	}
	for _, fenced := range extractFencedJSON(raw) {
		if analysis, ok := parseShellAnalysisJSON(fenced); ok {
			return analysis, true
		}
	}
	for _, candidate := range extractJSONObjectCandidates(raw) {
		if analysis, ok := parseShellAnalysisJSON(candidate); ok {
			return analysis, true
		}
	}
	return shellCommandAnalysis{}, false
}

func parseShellAnalysisJSON(raw string) (shellCommandAnalysis, bool) {
	var parsed struct {
		NeedsReview  *bool    `json:"needs_review"`
		AffectedDirs []string `json:"affected_dirs"`
		Reason       string   `json:"reason"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return shellCommandAnalysis{}, false
	}
	if parsed.NeedsReview == nil {
		return shellCommandAnalysis{}, false
	}
	return shellCommandAnalysis{
		NeedsReview:  *parsed.NeedsReview,
		AffectedDirs: parsed.AffectedDirs,
		Reason:       parsed.Reason,
	}, true
}

func extractFencedJSON(raw string) []string {
	matches := reJSONFence.FindAllStringSubmatch(raw, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			result = append(result, strings.TrimSpace(match[1]))
		}
	}
	return result
}

func extractJSONObjectCandidates(raw string) []string {
	var result []string
	start := -1
	depth := 0
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch ch {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		switch ch {
		case '"':
			if depth > 0 {
				inString = true
			}
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth == 0 {
				continue
			}
			depth--
			if depth == 0 && start >= 0 {
				result = append(result, raw[start:i+1])
				start = -1
			}
		}
	}
	return result
}

func classifyResponse(raw string) bool {
	upper := strings.ToUpper(raw)
	hasYes := reYes.MatchString(upper)
	hasNo := reNo.MatchString(upper)
	return !hasNo || hasYes
}

var reYes = regexp.MustCompile(`\bYES\b`)
var reNo = regexp.MustCompile(`\bNO\b`)
var reJSONFence = regexp.MustCompile("(?is)```(?:json)?\\s*(.*?)\\s*```")
var reShellMutable = regexp.MustCompile(`(?i)` +
	`\b(rm|mv|cp|mkdir|touch|chmod|chown|dd|tee|Remove-Item|Set-Content|Add-Content|Out-File|New-Item|Copy-Item|Move-Item|Invoke-WebRequest|Invoke-RestMethod)\s` +
	`|\b(git)\s+(add|commit|push|merge|rebase|stash)\b` +
	`|\b(pip|pip3|npm|apt|apt-get|yum|brew|cargo|go)\s+install` +
	`|\b(>|>>)\s*/\S` +
	`|sed\s+-i` +
	`|-EncodedCommand\b`,
)

func isObviouslyMutable(command string) bool {
	return reShellMutable.MatchString(command)
}

// shellClassifyCacheCapacity bounds the in-memory cache of shell classifier
// results so a long chat session that issues many distinct mutable commands
// cannot grow the cache without limit. The cache stores facts about the
// command (NeedsReview / AffectedDirs / Reason); the approval decision is
// recomputed every call by the review layer based on workspace + saved
// rules + review-mode, so changing those at runtime never observes stale
// decisions through the cache.
const shellClassifyCacheCapacity = 256

// shellClassifyLRU is a small bounded LRU that maps the classifier cache key
// to its shellCommandAnalysis. It uses container/list for O(1) move-to-front
// and a map for O(1) lookup, guarded by mu so concurrent classify calls from
// background tea.Cmd goroutines are safe.
type shellClassifyLRU struct {
	mu       sync.Mutex
	capacity int
	items    map[string]*list.Element
	order    *list.List // front = most recently used
}

type shellClassifyEntry struct {
	key   string
	value shellCommandAnalysis
}

func newShellClassifyLRU(capacity int) *shellClassifyLRU {
	if capacity <= 0 {
		capacity = shellClassifyCacheCapacity
	}
	return &shellClassifyLRU{
		capacity: capacity,
		items:    make(map[string]*list.Element, capacity),
		order:    list.New(),
	}
}

func (c *shellClassifyLRU) Load(key string) (shellCommandAnalysis, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return shellCommandAnalysis{}, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*shellClassifyEntry).value, true
}

func (c *shellClassifyLRU) Store(key string, value shellCommandAnalysis) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem, ok := c.items[key]; ok {
		elem.Value.(*shellClassifyEntry).value = value
		c.order.MoveToFront(elem)
		return
	}
	elem := c.order.PushFront(&shellClassifyEntry{key: key, value: value})
	c.items[key] = elem
	if c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest != nil {
			c.order.Remove(oldest)
			delete(c.items, oldest.Value.(*shellClassifyEntry).key)
		}
	}
}

// Len reports the current number of cached entries. Exposed for tests.
func (c *shellClassifyLRU) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

var shellClassifyCache = newShellClassifyLRU(shellClassifyCacheCapacity)
