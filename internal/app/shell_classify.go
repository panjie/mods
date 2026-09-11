package app

// LLM shell classifier: the fail-closed completion used when the deterministic
// analysis in shell_assess.go cannot prove an effect, plus the bounded cache and
// the tolerant parsers for the classifier's response. The authorisation
// decision itself is never made here; this only supplies facts.

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

const defaultShellClassifyPrompt = prompts.ShellClassifier

// classifyShellWithLLM sends the tool+command to the configured LLM for
// classification and caches the result. On any failure (timeout, stream
// error, parse error) it returns the fail-closed default.
func (m *Mods) classifyShellWithLLM(tool, command string) approval.CommandAssessment {
	cwd, _ := m.shellClassifierPathContext()
	return m.classifyShellAtCwd(tool, command, cwd)
}

func (m *Mods) classifyShellAtCwd(tool, command, cwd string) approval.CommandAssessment {
	system, structured, err := m.resolveShellClassifierPrompt()
	if err != nil {
		debug.Printf("assessCommand: prompt override failed: %v", err)
		return approval.UnknownCommandAssessment()
	}
	parseMode := "json"
	if !structured {
		parseMode = "yesno"
	}
	_, home := m.shellClassifierPathContext()
	userMessage, pathContext := shellClassifierUserMessage(tool, command, structured, cwd, home)
	cacheKey := shellClassifyCacheKey(tool, command, parseMode, system, pathContext)
	if cached, ok := shellClassifyCache.Load(cacheKey); ok {
		debug.Printf("assessCommand: cmd=%q cached -> effect=%s dirs=%v", debug.Truncate(command, 80), cached.Effect, cached.KnownDirs)
		return cached
	}

	cfg := m.Config
	api, mod, err := m.resolveModel(cfg)
	if err != nil {
		return approval.UnknownCommandAssessment()
	}

	cfgs, err := m.buildProviderConfigs(mod, api)
	if err != nil {
		return approval.UnknownCommandAssessment()
	}
	accfg := cfgs.Anthropic
	gccfg := cfgs.Google
	occfg := cfgs.Ollama
	ccfg := cfgs.OpenAI
	applyThinkConfigsWithOllama(mod, &gccfg, &accfg, &occfg, &ccfg, false)

	classifyCtx, cancel := context.WithTimeout(m.ctx, 5*time.Second)
	defer cancel()

	debug.Printf("assessCommand: using model=%s api=%s, structured=%v, system=%q", mod.Name, mod.API, structured, system)
	maxTokens := int64(256)
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
		return approval.UnknownCommandAssessment()
	}

	st := client.Request(classifyCtx, request)
	defer func() { _ = st.Close() }()

	var sb strings.Builder
	for st.Next() {
		chunk, err := st.Current()
		if err != nil && !errors.Is(err, stream.ErrNoContent) {
			return approval.UnknownCommandAssessment()
		}
		sb.WriteString(chunk.Content)
	}
	if st.Err() != nil {
		return approval.UnknownCommandAssessment()
	}
	rawResponse := strings.TrimSpace(sb.String())
	var assessment approval.CommandAssessment
	if structured {
		var ok bool
		assessment, ok = parseShellAssessmentResponse(rawResponse)
		if !ok {
			defaultResult := approval.UnknownCommandAssessment()
			shellClassifyCache.Store(cacheKey, defaultResult)
			return defaultResult
		}
	} else {
		effect, ok := parseLegacyShellEffect(rawResponse)
		if !ok {
			assessment = approval.UnknownCommandAssessment()
		} else if effect == approval.EffectWrite {
			assessment = approval.CommandAssessment{Effect: effect, Reason: "legacy classifier requested review"}
		} else {
			assessment = approval.CommandAssessment{Effect: effect, Reason: "legacy classifier reported read-only"}
		}
	}
	debug.Printf("assessCommand: cmd=%q resp=%s -> effect=%s dirs=%v reason=%q",
		command, debug.Truncate(rawResponse, 80), assessment.Effect, assessment.KnownDirs, assessment.Reason)

	shellClassifyCache.Store(cacheKey, assessment)
	return assessment
}

func (m *Mods) shellClassifierPathContext() (cwd, home string) {
	if m != nil && m.Config != nil {
		cwd = m.Config.ResolveWorkingDir().Canonical
	}
	home, _ = os.UserHomeDir()
	return strings.TrimSpace(cwd), strings.TrimSpace(home)
}

func shellClassifierUserMessage(tool, command string, structured bool, cwd, home string) (message, pathContext string) {
	if !structured {
		return fmt.Sprintf("Tool: %s\nCommand:\n%s", tool, command), ""
	}
	pathContext = strings.Join([]string{cwd, home}, "\x00")
	// Encode command and context separately so command text cannot forge envelope fields.
	data, _ := json.Marshal(struct {
		Tool       string
		WorkingDir string
		Home       string
		Command    string
	}{tool, classifierContextValue(cwd), classifierContextValue(home), command})
	return string(data), pathContext
}

func classifierContextValue(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func shellClassifyCacheKey(tool, command, parseMode, system, pathContext string) string {
	return strings.Join([]string{tool, command, parseMode, system, pathContext}, "\x00")
}

func (m *Mods) resolveShellClassifierPrompt() (string, bool, error) {
	if m.Config != nil && strings.TrimSpace(m.Config.Prompts.ShellClassifier) != "" {
		system, err := m.resolvePrompt(prompts.KeyShellClassifier, defaultShellClassifyPrompt)
		return system, true, err
	}
	if m.Config != nil && m.Config.ShellClassifyPrompt != "" {
		return m.Config.ShellClassifyPrompt, false, nil
	}
	return defaultShellClassifyPrompt, true, nil
}

func parseShellAssessmentResponse(raw string) (approval.CommandAssessment, bool) {
	if assessment, ok := parseShellAssessmentJSON(strings.TrimSpace(raw)); ok {
		return assessment, true
	}
	for _, fenced := range extractFencedJSON(raw) {
		if assessment, ok := parseShellAssessmentJSON(fenced); ok {
			return assessment, true
		}
	}
	for _, candidate := range extractJSONObjectCandidates(raw) {
		if assessment, ok := parseShellAssessmentJSON(candidate); ok {
			return assessment, true
		}
	}
	return approval.CommandAssessment{}, false
}

func parseShellAssessmentJSON(raw string) (approval.CommandAssessment, bool) {
	var parsed struct {
		LegacyReviewFlag *bool    `json:"needs_review"`
		AffectedDirs     []string `json:"affected_dirs"`
		Reason           string   `json:"reason"`
		Effect           string   `json:"effect"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &parsed); err != nil {
		return approval.CommandAssessment{}, false
	}
	effect := approval.CommandEffect(strings.ToLower(strings.TrimSpace(parsed.Effect)))
	if effect == "" && parsed.LegacyReviewFlag != nil {
		if *parsed.LegacyReviewFlag {
			effect = approval.EffectWrite
		} else {
			effect = approval.EffectRead
		}
	}
	if effect != approval.EffectRead && effect != approval.EffectWrite && effect != approval.EffectUnknown {
		return approval.CommandAssessment{}, false
	}
	if parsed.LegacyReviewFlag != nil {
		consistent := !*parsed.LegacyReviewFlag && effect == approval.EffectRead || *parsed.LegacyReviewFlag && effect != approval.EffectRead
		if !consistent {
			return approval.CommandAssessment{}, false
		}
	}
	knownDirs := make([]string, 0, len(parsed.AffectedDirs))
	for _, dir := range parsed.AffectedDirs {
		if validClassifierDir(dir) {
			knownDirs = append(knownDirs, strings.TrimSpace(dir))
		}
	}
	return approval.CommandAssessment{
		Effect:    effect,
		KnownDirs: knownDirs,
		Reason:    parsed.Reason,
	}, true
}

func validClassifierDir(dir string) bool {
	dir = strings.TrimSpace(dir)
	if dir == "" || strings.ContainsAny(dir, "\r\n<>") {
		return false
	}
	if approval.IsUnresolvedShellPathExpression(dir, true) || approval.IsUnresolvedShellPathExpression(dir, false) {
		return false
	}
	lower := strings.ToLower(dir)
	return lower != "unknown" && lower != "n/a" && lower != "none"
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

func parseLegacyShellEffect(raw string) (approval.CommandEffect, bool) {
	upper := strings.ToUpper(raw)
	hasYes := reYes.MatchString(upper)
	hasNo := reNo.MatchString(upper)
	if hasYes == hasNo {
		return approval.EffectUnknown, false
	}
	if hasYes {
		return approval.EffectWrite, true
	}
	return approval.EffectRead, true
}

var reYes = regexp.MustCompile(`\bYES\b`)
var reNo = regexp.MustCompile(`\bNO\b`)
var reJSONFence = regexp.MustCompile("(?is)```(?:json)?\\s*(.*?)\\s*```")

// shellClassifyCacheCapacity bounds the in-memory cache of shell classifier
// results so a long chat session that issues many distinct mutable commands
// cannot grow the cache without limit. The cache stores facts about the
// LLM completion only (Effect / KnownDirs / Reason); parser-derived shape,
// dynamic targets, and reviewability are recomputed for every call.
const shellClassifyCacheCapacity = 256

// shellClassifyLRU is a small bounded LRU that maps the classifier cache key
// to its LLM-only CommandAssessment completion. It uses container/list for O(1) move-to-front
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
	value approval.CommandAssessment
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

func (c *shellClassifyLRU) Load(key string) (approval.CommandAssessment, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem, ok := c.items[key]
	if !ok {
		return approval.CommandAssessment{}, false
	}
	c.order.MoveToFront(elem)
	return elem.Value.(*shellClassifyEntry).value, true
}

func (c *shellClassifyLRU) Store(key string, value approval.CommandAssessment) {
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
