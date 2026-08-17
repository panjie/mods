package app

import (
	"container/list"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/panjie/mods/internal/approval"
	"github.com/panjie/mods/internal/pathutil"
	"github.com/panjie/mods/internal/prompts"
	"github.com/panjie/mods/internal/proto"
	"github.com/panjie/mods/internal/stream"
	toolregistry "github.com/panjie/mods/internal/tools"
	"mvdan.cc/sh/v3/syntax"
)

const defaultShellClassifyPrompt = prompts.ShellClassifier

func shellPathFlavor(tool string) pathutil.Flavor {
	if shellToolUsesPowerShell(tool) {
		return pathutil.FlavorPowerShell
	}
	return pathutil.FlavorPOSIX
}

func shellToolUsesPowerShell(tool string) bool {
	return tool == "powershell_run" || ((tool == "shell_run" || tool == "process_run") && runtime.GOOS == "windows")
}

func (m *Mods) assessCommand(tool, command string) approval.CommandAssessment {
	if tool == "process_run" {
		return m.assessProcessInvocation(command)
	}
	ws := ""
	if m.Config != nil {
		ws = m.Config.ResolveWorkspace().Canonical
	}
	policy := m.readOnlyCommandPolicy()

	flavor := shellPathFlavor(tool)
	result := approval.AssessShellStaticWithPolicy(command, !shellToolUsesPowerShell(tool), policy)
	staticEffect := result.Effect
	externalPaths := filterArgPaths(result.KnownDirs, ws, flavor)
	// Keep a syntax-independent external-path fallback for both shell dialects.
	// Command-specific target extraction is intentionally conservative and can
	// miss valid option forms; an omitted external literal must never silently
	// collapse to the workspace approval scope.
	extractedPaths, hasBareHome := extractExternalPathFactsWithPolicy(command, ws, flavor, policy)
	externalPaths = appendMissingShellDirs(externalPaths, extractedPaths)
	if result.Effect == approval.EffectWrite {
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, externalPaths)
	} else {
		result.KnownDirs = externalPaths
	}
	if result.Effect == approval.EffectUnknown {
		completion := approval.UnknownCommandAssessment()
		if m.shellAnalyzer != nil {
			completion = m.shellAnalyzer(tool, command)
		} else {
			completion = m.classifyShellWithLLM(tool, command)
		}
		if hasBareHome {
			// The child shell and path normalizer share HOME. Once an unquoted
			// bare tilde has been resolved locally, classifier-supplied paths are
			// guesses and must not replace or broaden that deterministic scope.
			completion.KnownDirs = nil
		}
		result = mergeCommandAssessment(result, completion)
	}
	// A statically proven read with no explicit external target operates in the
	// configured workspace context. Classifier-completed commands do not get
	// this fallback: cwd is execution context, not evidence of an affected dir.
	if staticEffect == approval.EffectRead && len(result.KnownDirs) == 0 && len(result.DynamicTargets) == 0 && strings.TrimSpace(ws) != "" {
		result.KnownDirs = []string{ws}
	}
	return finalizeCommandAssessment(result, flavor)
}

func mergeCommandAssessment(static, completion approval.CommandAssessment) approval.CommandAssessment {
	if static.Effect != approval.EffectUnknown {
		return static
	}
	if completion.Effect != approval.EffectRead && completion.Effect != approval.EffectWrite {
		return static
	}
	static.Effect = completion.Effect
	static.KnownDirs = appendMissingShellDirs(static.KnownDirs, completion.KnownDirs)
	if strings.TrimSpace(completion.Reason) != "" {
		static.Reason = completion.Reason
	}
	return static
}

func finalizeCommandAssessment(result approval.CommandAssessment, flavor pathutil.Flavor) approval.CommandAssessment {
	result.KnownDirs, result.DynamicTargets = partitionShellAnalysisPaths(
		result.KnownDirs,
		result.DynamicTargets,
		flavor,
	)
	return finalizeAssessmentReviewability(result)
}

// finalizeProcessAssessment deliberately skips shell-expression partitioning:
// process_run arguments are literal, so values such as $HOME/out.txt remain
// concrete cwd-relative paths rather than becoming runtime shell targets.
func finalizeProcessAssessment(result approval.CommandAssessment) approval.CommandAssessment {
	return finalizeAssessmentReviewability(result)
}

func finalizeAssessmentReviewability(result approval.CommandAssessment) approval.CommandAssessment {
	reviewability := result.Reviewability
	if result.Effect == approval.EffectWrite && len(result.DynamicTargets) > 0 {
		reviewability.Level = approval.ReviewabilityCompound
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, approval.ReviewabilityDynamicWriteTarget)
		reviewability.ShouldCorrect = true
	}
	if result.Effect == approval.EffectRead && reviewabilityOnlyRecommendsProcess(reviewability) {
		// Keep the process_run recommendation in presentation metadata, but do
		// not spend the request's single corrective round on a harmless read.
		reviewability.ShouldCorrect = false
	}
	if len(result.DynamicTargets) > 1 {
		reviewability.Reasons = appendReviewabilityReason(reviewability.Reasons, approval.ReviewabilityMultipleDynamicTargets)
	}
	result.Reviewability = reviewability
	return result
}

func reviewabilityOnlyRecommendsProcess(reviewability approval.CommandReviewability) bool {
	return len(reviewability.Reasons) == 1 && reviewability.Reasons[0] == approval.ReviewabilitySingleProgramInShell
}

func appendReviewabilityReason(reasons []approval.ReviewabilityReason, reason approval.ReviewabilityReason) []approval.ReviewabilityReason {
	for _, existing := range reasons {
		if existing == reason {
			return reasons
		}
	}
	return append(reasons, reason)
}

func (m *Mods) assessProcessInvocation(raw string) approval.CommandAssessment {
	var invocation struct {
		Program string   `json:"program"`
		Args    []string `json:"args"`
		Cwd     string   `json:"cwd"`
	}
	if err := json.Unmarshal([]byte(raw), &invocation); err != nil || strings.TrimSpace(invocation.Program) == "" {
		return approval.UnknownCommandAssessment()
	}
	workspace := ""
	if m.Config != nil {
		workspace = m.Config.ResolveWorkspace().Canonical
	}
	cwd := strings.TrimSpace(invocation.Cwd)
	if cwd == "" {
		cwd = workspace
	} else if !pathutil.IsAbs(cwd) {
		cwd = pathutil.NormalizeShellPath(cwd, pathutil.DefaultOptions(workspace, shellPathFlavor("process_run")))
	}
	flavor := shellPathFlavor("process_run")
	posix := !shellToolUsesPowerShell("process_run")
	policy := m.readOnlyCommandPolicy()
	pathArgs := append([]string{invocation.Program}, invocation.Args...)
	explicitDirs := filterLiteralArgPaths(pathArgs, cwd, flavor)
	result := approval.AssessArgvStaticWithPolicy(invocation.Program, invocation.Args, posix, policy)
	staticDirs := append([]string(nil), result.KnownDirs...)
	switch result.Effect {
	case approval.EffectRead:
		// A statically proven direct read may use cwd as its implicit target
		// (for example git status), so retain the existing execution-context
		// scope for this proven case.
		result.KnownDirs = appendMissingShellDirs([]string{cwd}, explicitDirs)
	case approval.EffectWrite:
		writeDirs := normalizeLiteralProcessDirs(staticDirs, cwd, flavor)
		result.KnownDirs = appendMissingShellDirs(writeDirs, explicitDirs)
	default:
		classifierInput, _ := json.Marshal(map[string]any{
			"kind":    "direct_process_invocation",
			"program": invocation.Program,
			"args":    invocation.Args,
			"cwd":     cwd,
			"note":    "Arguments are literal; no shell expansion, pipeline, redirection, globbing, or variable interpolation occurs.",
		})
		completion := approval.UnknownCommandAssessment()
		if m.shellAnalyzer != nil {
			completion = m.shellAnalyzer("process_run", string(classifierInput))
		} else {
			completion = m.classifyShellWithLLM("process_run", string(classifierInput))
		}
		// An LLM may recognize that a program mutates state, but it cannot
		// safely bound an arbitrary executable's filesystem effects. In
		// particular, cwd in the classifier input is execution context rather
		// than evidence that the workspace is the mutation target. Preserve
		// effect/reason completion while deriving process directories only from
		// deterministic argv analysis.
		completion.KnownDirs = nil
		result = mergeCommandAssessment(result, completion)
		result.KnownDirs = appendMissingShellDirs(result.KnownDirs, explicitDirs)
	}
	// An executable given by an explicit relative or absolute path that lives
	// inside the workspace or a safe directory stays reviewable even when the
	// classifier calls it read-only: the script itself may do anything once it
	// runs. Bare names resolved into those directories are handled separately
	// via the pinned PATH binding in constrainResolvedProcessAssessment.
	if (result.Effect == approval.EffectRead || result.Effect == approval.EffectWrite) &&
		(strings.ContainsAny(invocation.Program, `/\`) || pathutil.IsAbs(invocation.Program)) {
		resolved := strings.TrimSpace(invocation.Program)
		if !pathutil.IsAbs(resolved) {
			resolved = pathutil.NormalizeShellPath(resolved, pathutil.DefaultOptions(cwd, flavor))
		}
		switch pathutil.Location(resolved, workspace, m.safeDirs()) {
		case pathutil.LocationWorkspace, pathutil.LocationSafe:
			result.Effect = approval.EffectUnknown
			result.Reason = "executable resolves from a workspace or temporary directory"
		}
	}
	return finalizeProcessAssessment(result)
}

func (m *Mods) constrainResolvedProcessAssessment(result approval.CommandAssessment, binding toolregistry.ProcessProgramBinding) approval.CommandAssessment {
	if binding.Resolved == "" || m == nil || m.Config == nil {
		return result
	}
	workspace := m.Config.ResolveWorkspace().Canonical
	location := pathutil.Location(binding.Resolved, workspace, m.safeDirs())
	if location != pathutil.LocationWorkspace && location != pathutil.LocationSafe {
		return result
	}
	result.Effect = approval.EffectUnknown
	result.Reason = "executable resolves from a workspace or temporary directory"
	return result
}

func normalizeLiteralProcessDirs(dirs []string, cwd string, flavor pathutil.Flavor) []string {
	if len(dirs) == 0 {
		return nil
	}
	result := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		if normalized := normalizeLiteralProcessPath(dir, cwd, flavor); normalized != "" {
			result = appendMissingShellDirs(result, []string{normalized})
		}
	}
	return result
}

func filterLiteralArgPaths(args []string, cwd string, flavor pathutil.Flavor) []string {
	var result []string
	for _, arg := range args {
		arg = strings.Trim(strings.TrimSpace(arg), `"'`)
		if _, value, ok := strings.Cut(arg, "="); ok && isExplicitShellPathArg(value) {
			arg = value
		}
		if !literalArgLooksPathLike(arg, flavor) {
			continue
		}
		normalized := normalizeLiteralProcessPath(arg, cwd, flavor)
		if pathutil.Location(normalized, cwd, nil) == pathutil.LocationExternal {
			result = appendMissingShellDirs(result, []string{normalized})
		}
	}
	return result
}

func literalArgLooksPathLike(arg string, flavor pathutil.Flavor) bool {
	if arg == "" {
		return false
	}
	if pathutil.IsAbs(arg) || strings.HasPrefix(arg, "./") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `.\`) || strings.HasPrefix(arg, `..\`) {
		return true
	}
	if flavor == pathutil.FlavorPowerShell {
		return strings.ContainsAny(arg, `/\`)
	}
	return strings.Contains(arg, "/")
}

func normalizeLiteralProcessPath(value, cwd string, flavor pathutil.Flavor) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if !pathutil.IsAbs(value) {
		// Prefixing with an explicit current-directory segment prevents pathutil
		// from interpreting shell-looking literals such as $HOME/x or ~/x.
		if flavor == pathutil.FlavorPowerShell {
			value = `.\` + value
		} else {
			value = "./" + value
		}
	}
	return pathutil.NormalizePath(value, pathutil.Options{Workspace: cwd, Flavor: flavor})
}

func (m *Mods) readOnlyCommandPolicy() approval.ReadOnlyCommandPolicy {
	if m == nil || m.Config == nil {
		return approval.ReadOnlyCommandPolicy{}
	}
	return approval.ReadOnlyCommandPolicy{
		Commands: m.Config.BuiltinTools.ShellReadOnlyCommands,
	}
}

func partitionShellAnalysisPaths(dirs, unresolved []string, flavor pathutil.Flavor) ([]string, []string) {
	posix := flavor != pathutil.FlavorPowerShell
	var concrete []string
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if approval.IsUnresolvedShellPathExpression(dir, posix) {
			unresolved = append(unresolved, dir)
			continue
		}
		concrete = appendMissingShellDirs(concrete, []string{dir})
	}
	var dynamic []string
	seen := map[string]struct{}{}
	for _, expr := range unresolved {
		expr = strings.TrimSpace(expr)
		if expr == "" {
			continue
		}
		key := expr
		if flavor == pathutil.FlavorPowerShell {
			key = strings.ToLower(key)
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dynamic = append(dynamic, expr)
	}
	return concrete, dynamic
}

func appendMissingShellDirs(dirs []string, extra []string) []string {
	for _, p := range extra {
		found := false
		for _, d := range dirs {
			if d == p {
				found = true
				break
			}
		}
		if !found {
			dirs = append(dirs, p)
		}
	}
	return dirs
}

// classifyShellWithLLM sends the tool+command to the configured LLM for
// classification and caches the result. On any failure (timeout, stream
// error, parse error) it returns the fail-closed default.
func (m *Mods) classifyShellWithLLM(tool, command string) approval.CommandAssessment {
	system, structured, err := m.resolveShellClassifierPrompt()
	if err != nil {
		debug.Printf("assessCommand: prompt override failed: %v", err)
		return approval.UnknownCommandAssessment()
	}
	parseMode := "json"
	if !structured {
		parseMode = "yesno"
	}
	workspace, home := m.shellClassifierPathContext()
	userMessage, pathContext := shellClassifierUserMessage(tool, command, structured, workspace, home)
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

func (m *Mods) shellClassifierPathContext() (workspace, home string) {
	if m != nil && m.Config != nil {
		workspace = m.Config.ResolveWorkspace().Canonical
	}
	home, _ = os.UserHomeDir()
	return strings.TrimSpace(workspace), strings.TrimSpace(home)
}

func shellClassifierUserMessage(tool, command string, structured bool, workspace, home string) (message, pathContext string) {
	if !structured {
		return fmt.Sprintf("Tool: %s\nCommand:\n%s", tool, command), ""
	}
	pathContext = strings.Join([]string{workspace, home}, "\x00")
	return fmt.Sprintf(
		"Tool: %s\nExecution context (authoritative):\nWorkspace: %s\nHome: %s\nCommand:\n%s",
		tool, classifierContextValue(workspace), classifierContextValue(home), command,
	), pathContext
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

func classifyResponse(raw string) bool {
	effect, ok := parseLegacyShellEffect(raw)
	return !ok || effect == approval.EffectWrite
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

// Path-extraction patterns for extractExternalPaths. The *Path variants
// capture the full token so it can be populated into KnownDirs.
var (
	reParentPath        = regexp.MustCompile(`\.\.[\\/][^\s'"<>|;,&(){}]*`)
	reHomePath          = regexp.MustCompile(`~[\\/a-zA-Z][^\s'"<>|;,&(){}]*`)
	reHomeVarPath       = regexp.MustCompile(`(?i)\$(?:\{(?:HOME|env:USERPROFILE)\}|env:USERPROFILE|HOME)[\\/][^\s'"<>|;,&(){}]*`)
	reCMDHomePath       = regexp.MustCompile(`(?i)%(?:USERPROFILE|HOMEDRIVE%%HOMEPATH)%[\\/][^\s'"<>|;,&(){}]*`)
	reUnixAbsPath       = regexp.MustCompile(`(?:^|[\s="'"])(/(?:[A-Za-z0-9._][^\s'"<>|;,&(){}]*)?)`)
	reSingleQuoted      = regexp.MustCompile(`'[^']*'`)
	reDoubleQuotedValue = regexp.MustCompile(`"([^"\r\n]*)"`)
	reWinAbsPath        = regexp.MustCompile(`(?:^|[\s='"])([A-Za-z]:[\\/][^\s'"<>|;,&(){}]*)`)
	reWinUNCPath        = regexp.MustCompile(`(?:^|[\s='"])(\\\\[^\\/\s'"<>|;,&(){}]+[\\/][^\s'"<>|;,&(){}]*)`)
)

// extractExternalPaths returns path tokens from the command that reference
// locations outside the workspace: absolute paths not under workspaceDir,
// home-expanded paths (~/ and ~user), and parent-traversal paths (../).
// The results populate KnownDirs so ClassifyAccess and risk labels can
// correctly identify external access even when the LLM omits them.
func extractExternalPaths(command, workspaceDir string) []string {
	return extractExternalPathsWithFlavor(command, workspaceDir, pathutil.FlavorPOSIX)
}

func extractExternalPathsWithFlavor(command, workspaceDir string, flavor pathutil.Flavor) []string {
	return extractExternalPathsWithPolicy(command, workspaceDir, flavor, approval.ReadOnlyCommandPolicy{})
}

func extractExternalPathsWithPolicy(command, workspaceDir string, flavor pathutil.Flavor, policy approval.ReadOnlyCommandPolicy) []string {
	paths, _ := extractExternalPathFactsWithPolicy(command, workspaceDir, flavor, policy)
	return paths
}

func extractExternalPathFactsWithPolicy(command, workspaceDir string, flavor pathutil.Flavor, policy approval.ReadOnlyCommandPolicy) ([]string, bool) {
	originalCommand := command
	opts := pathutil.DefaultOptions(workspaceDir, flavor)
	seen := map[string]bool{}
	var paths []string
	add := func(p string) {
		p = pathutil.NormalizeShellPath(p, opts)
		if pathutil.Location(p, workspaceDir, nil) != pathutil.LocationExternal {
			return
		}
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		paths = append(paths, p)
	}

	if flavor == pathutil.FlavorPowerShell {
		// Windows/PowerShell branch: compiler flags (/out, /target,
		// /reference) share leading-slash syntax with Unix absolute paths,
		// while "/" is also PowerShell's division operator. Keep this branch
		// strictly on Windows/PowerShell path syntax so POSIX-looking tokens
		// are not misclassified as filesystem paths. Quoted strings are only
		// treated as paths when the full literal starts with explicit path syntax;
		// single-quoted strings are then stripped to avoid false positives from
		// script literals.
		addPowerShellQuotedPathArgs(command, add)
		command = stripPowerShellSingleQuotedStrings(command)
		command = reDoubleQuotedValue.ReplaceAllString(command, " ")
		for _, m := range reWinAbsPath.FindAllStringSubmatch(command, -1) {
			add(m[1])
		}
		for _, m := range reWinUNCPath.FindAllStringSubmatch(command, -1) {
			add(m[1])
		}
		for _, m := range reHomePath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reHomeVarPath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reCMDHomePath.FindAllString(command, -1) {
			add(m)
		}
		for _, m := range reParentPath.FindAllString(command, -1) {
			add(m)
		}
		return paths, false
	}

	// POSIX branch: Unix absolute paths (including single-segment like
	// /etc, /tmp) are valid filesystem references. Heredoc bodies are
	// blanked first so embedded path-like tokens don't produce false
	// positives, then single-quoted script literals are stripped.
	command = blankPOSIXHeredocBodies(command)
	command = reSingleQuoted.ReplaceAllString(command, " ")
	for _, m := range reUnixAbsPath.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}
	for _, m := range reWinAbsPath.FindAllStringSubmatch(command, -1) {
		add(m[1])
	}
	for _, m := range reHomePath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reHomeVarPath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reCMDHomePath.FindAllString(command, -1) {
		add(m)
	}
	for _, m := range reParentPath.FindAllString(command, -1) {
		add(m)
	}
	hasBareHome := approval.POSIXHasUnquotedBareHomeArg(originalCommand)
	if hasBareHome {
		add("~")
	}
	// The raw-text scan strips single-quoted programs to avoid interpreting
	// awk/sed regex syntax as paths. Recover genuine quoted path arguments
	// from the shell AST: only values that begin with explicit external-path
	// syntax are considered, so quoted program bodies remain ignored.
	if readOnly, _ := approval.IsReadOnlyPOSIXWithPolicy(originalCommand, policy); readOnly {
		for _, arg := range approval.StaticPOSIXLiteralArgs(originalCommand) {
			if isExplicitShellPathArg(arg) {
				add(arg)
			}
		}
	}
	return paths, hasBareHome
}

func addPowerShellQuotedPathArgs(command string, add func(string)) {
	for _, value := range powerShellSingleQuotedValues(command) {
		if isExplicitPowerShellPathArg(value) {
			add(value)
		}
	}
	for _, m := range reDoubleQuotedValue.FindAllStringSubmatch(command, -1) {
		if isExplicitPowerShellPathArg(m[1]) {
			add(m[1])
		}
	}
}

func powerShellSingleQuotedValues(command string) []string {
	var values []string
	for i := 0; i < len(command); i++ {
		if command[i] != '\'' {
			continue
		}
		var value strings.Builder
		for j := i + 1; j < len(command); j++ {
			if command[j] != '\'' {
				value.WriteByte(command[j])
				continue
			}
			if j+1 < len(command) && command[j+1] == '\'' {
				value.WriteByte('\'')
				j++
				continue
			}
			values = append(values, value.String())
			i = j
			break
		}
	}
	return values
}

func stripPowerShellSingleQuotedStrings(command string) string {
	var stripped strings.Builder
	for i := 0; i < len(command); i++ {
		if command[i] != '\'' {
			stripped.WriteByte(command[i])
			continue
		}
		closing := -1
		for j := i + 1; j < len(command); j++ {
			if command[j] != '\'' {
				continue
			}
			if j+1 < len(command) && command[j+1] == '\'' {
				j++
				continue
			}
			closing = j
			break
		}
		if closing == -1 {
			stripped.WriteByte(command[i])
			continue
		}
		stripped.WriteByte(' ')
		i = closing
	}
	return stripped.String()
}

func isExplicitShellPathArg(arg string) bool {
	if arg == "~" || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		return true
	}
	if reHomeVarPath.MatchString(arg) || reCMDHomePath.MatchString(arg) || reWinAbsPath.MatchString(" "+arg) {
		return true
	}
	return strings.HasPrefix(arg, "~") && (strings.Contains(arg, "/") || strings.Contains(arg, `\`))
}

func isExplicitPowerShellPathArg(arg string) bool {
	arg = strings.TrimSpace(arg)
	if arg == "~" || strings.HasPrefix(arg, "../") || strings.HasPrefix(arg, `..\`) || hasDotPrefixedParentTraversal(arg) || strings.HasPrefix(arg, "~/") || strings.HasPrefix(arg, `~\`) {
		return true
	}
	if len(arg) >= 3 && ((arg[0] >= 'A' && arg[0] <= 'Z') || (arg[0] >= 'a' && arg[0] <= 'z')) && arg[1] == ':' && (arg[2] == '\\' || arg[2] == '/') {
		return true
	}
	if isExplicitWindowsUNCPath(arg) {
		return true
	}
	lower := strings.ToLower(arg)
	for _, prefix := range []string{"${env:userprofile}", "$env:userprofile", "${home}", "$home", "%userprofile%", "%homedrive%%homepath%"} {
		if strings.HasPrefix(lower, prefix+`\`) || strings.HasPrefix(lower, prefix+"/") {
			return true
		}
	}
	return strings.HasPrefix(arg, "~") && (strings.Contains(arg, "/") || strings.Contains(arg, `\`))
}

func isExplicitWindowsUNCPath(arg string) bool {
	if !strings.HasPrefix(arg, `\\`) && !strings.HasPrefix(arg, `//`) {
		return false
	}
	rest := arg[2:]
	serverEnd := strings.IndexAny(rest, `/\`)
	if serverEnd <= 0 {
		return false
	}
	server := rest[:serverEnd]
	if strings.ContainsAny(server, " \t\r\n'\"<>|;,&(){}") {
		return false
	}
	shareAndRest := rest[serverEnd+1:]
	if shareAndRest == "" {
		return false
	}
	shareEnd := strings.IndexAny(shareAndRest, `/\`)
	share := shareAndRest
	if shareEnd >= 0 {
		share = shareAndRest[:shareEnd]
	}
	return share != "" && !strings.ContainsAny(share, `<>:"|?*`)
}

func hasDotPrefixedParentTraversal(arg string) bool {
	if len(arg) < len("./..") || arg[0] != '.' || !isShellPathSeparator(arg[1]) || arg[2] != '.' || arg[3] != '.' {
		return false
	}
	return len(arg) == len("./..") || isShellPathSeparator(arg[4])
}

func isShellPathSeparator(ch byte) bool {
	return ch == '/' || ch == '\\'
}

func blankPOSIXHeredocBodies(command string) string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return command
	}

	buf := []byte(command)
	syntax.Walk(file, func(node syntax.Node) bool {
		redir, ok := node.(*syntax.Redirect)
		if !ok || redir.Hdoc == nil {
			return true
		}
		startPos := redir.Hdoc.Pos()
		endPos := redir.Hdoc.End()
		if !startPos.IsValid() || !endPos.IsValid() {
			return true
		}
		blankRangePreserveLines(buf, int(startPos.Offset()), int(endPos.Offset()))
		return true
	})
	return string(buf)
}

func blankRangePreserveLines(buf []byte, start, end int) {
	if start < 0 {
		start = 0
	}
	if end > len(buf) {
		end = len(buf)
	}
	if start >= end {
		return
	}
	for i := start; i < end; i++ {
		if buf[i] != '\n' && buf[i] != '\r' {
			buf[i] = ' '
		}
	}
}

// mentionsExternalPath reports whether the command text references any path
// outside the workspace. It delegates to extractExternalPaths for the actual
// work and is retained as a thin wrapper for existing callers and tests.
func mentionsExternalPath(command, workspaceDir string) bool {
	return len(extractExternalPaths(command, workspaceDir)) > 0
}

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

// filterArgPaths filters parser-extracted argument values to explicit paths
// outside the workspace. It deliberately performs no shell parsing: the
// caller already derived these literals from the invocation's single AST/IR.
func filterArgPaths(args []string, workspaceDir string, flavor pathutil.Flavor) []string {
	if len(args) == 0 {
		return nil
	}
	var result []string
	seen := map[string]bool{}
	opts := pathutil.DefaultOptions(workspaceDir, flavor)
	add := func(p string) {
		p = pathutil.NormalizeShellPath(p, opts)
		if pathutil.Location(p, workspaceDir, nil) != pathutil.LocationExternal || seen[p] {
			return
		}
		seen[p] = true
		result = append(result, p)
	}
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if _, value, ok := strings.Cut(arg, "="); ok {
			arg = value
		}
		isExplicit := isExplicitShellPathArg(arg)
		if flavor == pathutil.FlavorPowerShell {
			isExplicit = isExplicitPowerShellPathArg(arg)
		}
		if isExplicit {
			add(arg)
		}
	}
	return result
}
