package app

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
)

const defaultShellClassifyPrompt = prompts.ShellClassifier

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

	wsRoot := ""
	if m.Config != nil {
		wsRoot = m.Config.ResolveWorkspaceRoot()
	}

	// Local fast-path: conservative allowlist of read-only commands with no
	// shell metacharacters and no external-path references. Everything else,
	// including read-only commands that touch external paths, goes to the
	// LLM classifier for precise analysis.
	if isSimpleReadOnly(command) && len(extractExternalPaths(command, wsRoot)) == 0 {
		debug.Printf("analyzeShellCommand: cmd=%q -> local: read-only, workspace-local", debug.Truncate(command, 80))
		return shellCommandAnalysis{
			NeedsReview: false,
			Reason:      "read-only command, workspace-local (local heuristic)",
		}
	}

	// LLM classifier
	result := m.classifyShellWithLLM(tool, command)

	// Post-process: merge regex-detected external paths into AffectedDirs
	// so external access is never silently dropped when the LLM omits dirs
	// (read-only commands) or fails entirely.
	for _, p := range extractExternalPaths(command, wsRoot) {
		found := false
		for _, d := range result.AffectedDirs {
			if d == p {
				found = true
				break
			}
		}
		if !found {
			result.AffectedDirs = append(result.AffectedDirs, p)
		}
	}

	return result
}

// classifyShellWithLLM sends the tool+command to the configured LLM for
// classification and caches the result. On any failure (timeout, stream
// error, parse error) it returns the fail-closed default.
func (m *Mods) classifyShellWithLLM(tool, command string) shellCommandAnalysis {
	system, structured, err := m.resolveShellClassifierPrompt()
	if err != nil {
		debug.Printf("analyzeShellCommand: prompt override failed: %v", err)
		return defaultShellCommandAnalysis()
	}
	parseMode := "json"
	if !structured {
		parseMode = "yesno"
	}
	cacheKey := shellClassifyCacheKey(tool, command, parseMode, system)
	if cached, ok := shellClassifyCache.Load(cacheKey); ok {
		debug.Printf("analyzeShellCommand: cmd=%q cached -> needsReview=%v dirs=%v", debug.Truncate(command, 80), cached.NeedsReview, cached.AffectedDirs)
		return cached
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

	debug.Printf("analyzeShellCommand: using model=%s api=%s, structured=%v, system=%q", mod.Name, mod.API, structured, system)
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
	if structured {
		var ok bool
		analysis, ok = parseShellAnalysisResponse(rawResponse)
		if !ok {
			// Don't cache parse failures — a single hallucination shouldn't
			// permanently mark this command as requiring review.
			return defaultShellCommandAnalysis()
		}
	} else {
		analysis = shellCommandAnalysis{NeedsReview: classifyResponse(rawResponse)}
	}
	debug.Printf("analyzeShellCommand: cmd=%q resp=%s -> needsReview=%v dirs=%v reason=%q",
		command, debug.Truncate(rawResponse, 80), analysis.NeedsReview, analysis.AffectedDirs, analysis.Reason)

	shellClassifyCache.Store(cacheKey, analysis)
	return analysis
}

func shellClassifyCacheKey(tool, command, parseMode, system string) string {
	return strings.Join([]string{tool, command, parseMode, system}, "\x00")
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
// readOnlyCommands is a conservative allowlist of shell commands whose basic
// form is read-only and has no file-modifying flags. Only commands in this
// set may skip the LLM classifier — and even then only when the command
// contains no shell metacharacters and touches no external paths.
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"wc": true, "file": true, "stat": true, "pwd": true,
	"echo": true, "date": true, "whoami": true, "hostname": true,
	"uname": true, "du": true, "df": true, "which": true,
	"env": true, "printenv": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true,
	"grep": true, "egrep": true, "fgrep": true,
}

// reShellMetacharacters matches shell metacharacters that can alter the
// semantics of an otherwise-safe command (redirection, piping, command
// chaining, substitution). Any hit forces the LLM classifier.
var reShellMetacharacters = regexp.MustCompile(`[>|;` + "`" + `]|&&|\$\(`)

func isSimpleReadOnly(command string) bool {
	if reShellMetacharacters.MatchString(command) {
		return false
	}
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return false
	}
	return readOnlyCommands[filepath.Base(fields[0])]
}

// Path-extraction patterns for extractExternalPaths. The *Path variants
// capture the full token so it can be populated into AffectedDirs.
var (
	reParentPath   = regexp.MustCompile(`\.\.[\\/][^\s'"<>|;,&(){}]*`)
	reHomePath     = regexp.MustCompile(`~[\\/a-zA-Z][^\s'"<>|;,&(){}]*`)
	reUnixAbsPath  = regexp.MustCompile(`(?:^|[\s="'"])(/(?:[A-Za-z0-9._][^\s'"<>|;,&(){}]*)?)`)
	reWinAbsPath   = regexp.MustCompile(`(?:^|[\s='"])([A-Za-z]:[\\/][^\s'"<>|;,&(){}]*)`)
)

// extractExternalPaths returns path tokens from the command that reference
// locations outside the workspace: absolute paths not under workspaceRoot,
// home-expanded paths (~/ and ~user), and parent-traversal paths (../).
// The results populate AffectedDirs so ClassifyAccess and risk labels can
// correctly identify external access even when the LLM omits them.
func extractExternalPaths(command, workspaceRoot string) []string {
	wsClean := filepath.Clean(workspaceRoot)
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}
	for _, m := range reUnixAbsPath.FindAllStringSubmatch(command, -1) {
		if !pathInsideWorkspace(m[1], wsClean) {
			add(m[1])
		}
	}
	for _, m := range reWinAbsPath.FindAllStringSubmatch(command, -1) {
		if !pathInsideWorkspace(m[1], wsClean) {
			add(m[1])
		}
	}
	for _, m := range reHomePath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reParentPath.FindAllString(command, -1) {
		add(m)
	}
	return paths
}

// mentionsExternalPath reports whether the command text references any path
// outside the workspace. It delegates to extractExternalPaths for the actual
// work and is retained as a thin wrapper for existing callers and tests.
func mentionsExternalPath(command, workspaceRoot string) bool {
	return len(extractExternalPaths(command, workspaceRoot)) > 0
}

func pathInsideWorkspace(target, root string) bool {
	if root == "" {
		return false
	}
	if isWindowsStylePath(target) != isWindowsStylePath(root) {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

func isWindowsStylePath(p string) bool {
	return len(p) >= 3 && p[1] == ':' && (p[2] == '\\' || p[2] == '/')
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
