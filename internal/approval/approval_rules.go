package approval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

type RuleType string
type ScopeKind string

const (
	ShellPrefix RuleType = "shell_prefix"
	ShellExact  RuleType = "shell_exact"
	EditAll     RuleType = "edit_all"
	ToolAll     RuleType = "tool_all"
	DirAllow    RuleType = "dir_allow"

	ScopeWorkspace ScopeKind = "workspace"
)

// Scope identifies the boundary within which an approval rule applies.
type Scope struct {
	Kind  ScopeKind
	Value string
}

// Rule is a scoped permission granted through the review UI.
type Rule struct {
	ScopeKind  ScopeKind `db:"scope_kind"`
	ScopeValue string    `db:"scope_value"`
	Type       RuleType  `db:"rule_type"`
	Tool       string    `db:"tool_name"`
	Pattern    string    `db:"pattern"`
	Paths      []string  `db:"paths"`
}

func WorkspaceScope(root string) Scope {
	return Scope{
		Kind:  ScopeWorkspace,
		Value: filepath.Clean(root),
	}
}

func (r Rule) key() string {
	pathsKey := strings.Join(r.Paths, "\x01")
	return string(r.ScopeKind) + "\x00" + r.ScopeValue + "\x00" +
		string(r.Type) + "\x00" + r.Tool + "\x00" + r.Pattern + "\x00" + pathsKey
}

func (r Rule) matchesScope(scope Scope) bool {
	if scope.Kind == "" || scope.Value == "" {
		return false
	}
	return r.ScopeKind == scope.Kind && r.ScopeValue == scope.Value
}

func (r Rule) String() string {
	switch r.Type {
	case ShellPrefix, ShellExact:
		return fmt.Sprintf("%s(%s)", r.Tool, r.Pattern)
	case EditAll:
		return "file edits"
	case DirAllow:
		return fmt.Sprintf("dirs: %s", strings.Join(r.Paths, ", "))
	case ToolAll:
		return r.Tool
	default:
		return r.Tool
	}
}

type RuleSet struct {
	mu    sync.RWMutex
	rules []Rule
}

func (s *RuleSet) Replace(rules []Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = Dedupe(rules)
}

func (s *RuleSet) Add(rules ...Rule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = Dedupe(append(s.rules, rules...))
}

func (s *RuleSet) Snapshot() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Rule(nil), s.rules...)
}

func (s *RuleSet) Allows(name string, data []byte, scope Scope) bool {
	rules := rulesForScope(s.Snapshot(), scope)
	switch name {
	case "fs_write_file", "fs_apply_patch":
		return slices.ContainsFunc(rules, func(rule Rule) bool {
			return rule.Type == EditAll
		})
	case "shell_run", "powershell_run":
		command := ExtractShellCommand(data)
		if command == "" {
			return false
		}
		return dirAllowForCommand(name, command, rules, scope.Value, shellToolUsesPOSIX(name))
	default:
		return slices.ContainsFunc(rules, func(rule Rule) bool {
			return rule.Type == ToolAll && rule.Tool == name
		})
	}
}

func Dedupe(rules []Rule) []Rule {
	seen := make(map[string]struct{}, len(rules))
	result := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.Tool == "" && rule.Type != DirAllow {
			continue
		}
		if _, ok := seen[rule.key()]; ok {
			continue
		}
		seen[rule.key()] = struct{}{}
		result = append(result, rule)
	}
	return result
}

func RulesFor(name string, data []byte, scope Scope) []Rule {
	return scopeRules(rulesForTool(name, data), scope)
}

func rulesForTool(name string, data []byte) []Rule {
	switch name {
	case "fs_write_file", "fs_apply_patch":
		return []Rule{{
			Type: EditAll,
			Tool: "file_edit",
		}}
	case "shell_run", "powershell_run":
		return dirAllowRulesForTool(name, ExtractShellCommand(data), shellToolUsesPOSIX(name))
	default:
		return []Rule{{
			Type: ToolAll,
			Tool: name,
		}}
	}
}

func scopeRules(rules []Rule, scope Scope) []Rule {
	if scope.Kind == "" || scope.Value == "" {
		return nil
	}
	result := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		rule.ScopeKind = scope.Kind
		rule.ScopeValue = scope.Value
		result = append(result, rule)
	}
	return result
}

func rulesForScope(rules []Rule, scope Scope) []Rule {
	result := make([]Rule, 0, len(rules))
	for _, rule := range rules {
		if rule.matchesScope(scope) {
			result = append(result, rule)
		}
	}
	return result
}

func RulesLabel(rules []Rule) string {
	if len(rules) == 0 {
		return "this operation"
	}
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		labels = append(labels, rule.String())
	}
	return strings.Join(labels, ", ")
}

func ShellRulesWithMode(command string, posix bool) []Rule {
	return ShellRulesForToolWithMode("shell_run", command, posix)
}

func ShellRulesForToolWithMode(tool, command string, posix bool) []Rule {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if tool == "powershell_run" && commandHasPowerShellCompoundSyntax(normalized) {
		return []Rule{shellExactRule(tool, normalized)}
	}
	if !posix {
		return shellApprovalRulesSimple(tool, normalized)
	}
	leaves, ok := parseShellLeaves(command)
	if !ok || len(leaves) == 0 || len(leaves) > 5 {
		return []Rule{shellExactRule(tool, normalized)}
	}
	rules := make([]Rule, 0, len(leaves))
	for _, leaf := range leaves {
		rules = append(rules, ruleForShellLeaf(tool, leaf))
	}
	return Dedupe(rules)
}

func shellApprovalRulesSimple(tool, normalized string) []Rule {
	parts := splitSimpleCompound(normalized)
	if len(parts) == 0 || len(parts) > 5 {
		return []Rule{shellExactRule(tool, normalized)}
	}
	var rules []Rule
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if hasShellRedirection(part) {
			rules = append(rules, shellExactRule(tool, part))
			continue
		}
		tokens := tokenizeSimple(part)
		if len(tokens) == 0 {
			continue
		}
		rules = append(rules, ruleFromTokens(tool, tokens, false, part))
	}
	return Dedupe(rules)
}

func ShellAllowWithMode(command string, rules []Rule, posix bool) bool {
	return ShellAllowForToolWithMode("shell_run", command, rules, posix)
}

func ExtractShellCommand(args []byte) string {
	var parsed struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &parsed); err != nil {
		return ""
	}
	return parsed.Command
}

func ShellAllowForToolWithMode(tool, command string, rules []Rule, posix bool) bool {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return false
	}
	if slices.ContainsFunc(rules, func(rule Rule) bool {
		return rule.Type == ShellExact && rule.Tool == tool && rule.Pattern == normalized
	}) {
		return true
	}
	if tool == "powershell_run" && commandHasPowerShellCompoundSyntax(normalized) {
		return false
	}
	if !posix {
		return shellRulesAllowSimple(tool, normalized, rules)
	}
	leaves, ok := parseShellLeaves(command)
	if !ok || len(leaves) == 0 {
		return false
	}
	for _, leaf := range leaves {
		if !slices.ContainsFunc(rules, func(rule Rule) bool {
			if rule.Tool != tool {
				return false
			}
			switch rule.Type {
			case ShellExact:
				return rule.Pattern == leaf.text
			case ShellPrefix:
				return matchShellPrefix(rule.Pattern, leaf.text)
			default:
				return false
			}
		}) {
			return false
		}
	}
	return true
}

func shellRulesAllowSimple(tool, normalized string, rules []Rule) bool {
	parts := splitSimpleCompound(normalized)
	for _, part := range parts {
		commandText := strings.TrimSpace(part)
		if commandText == "" {
			return false
		}
		found := false
		for _, rule := range rules {
			if rule.Tool != tool {
				continue
			}
			switch rule.Type {
			case ShellExact:
				if rule.Pattern == commandText {
					found = true
				}
			case ShellPrefix:
				if matchShellPrefix(rule.Pattern, commandText) {
					found = true
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func matchShellPrefix(pattern, command string) bool {
	base := strings.TrimSuffix(pattern, " *")
	return command == base || strings.HasPrefix(command, base+" ")
}

type shellLeaf struct {
	text  string
	call  *syntax.CallExpr
	exact bool
}

func parseShellLeaves(command string) ([]shellLeaf, bool) {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil, false
	}
	var leaves []shellLeaf
	for _, stmt := range file.Stmts {
		if !collectShellLeaves(stmt, &leaves) {
			return nil, false
		}
	}
	return leaves, true
}

func collectShellLeaves(stmt *syntax.Stmt, leaves *[]shellLeaf) bool {
	if stmt == nil || stmt.Cmd == nil {
		return false
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		if stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 {
			return false
		}
		return collectShellLeaves(binary.X, leaves) && collectShellLeaves(binary.Y, leaves)
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok {
		return false
	}
	text, ok := printShellNode(stmt)
	if !ok || text == "" {
		return false
	}
	exact := stmt.Negated || stmt.Background || len(stmt.Redirs) > 0 || len(call.Assigns) > 0
	if shellNodeHasDynamicParts(stmt) {
		exact = true
	}
	*leaves = append(*leaves, shellLeaf{
		text:  text,
		call:  call,
		exact: exact,
	})
	return true
}

func printShellNode(node syntax.Node) (string, bool) {
	var buf bytes.Buffer
	printer := syntax.NewPrinter(syntax.SingleLine(true))
	if err := printer.Print(&buf, node); err != nil {
		return "", false
	}
	return strings.TrimSpace(buf.String()), true
}

func normalizeShellCommand(command string) string {
	return normalizeShellCommandWithMode(command, shellToolUsesPOSIX("shell_run"))
}

func normalizeShellCommandWithMode(command string, posix bool) string {
	command = strings.TrimSpace(command)
	if command == "" || !posix {
		return command
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return command
	}
	normalized, ok := printShellNode(file)
	if !ok {
		return command
	}
	return normalized
}

func shellNodeHasDynamicParts(node syntax.Node) bool {
	dynamic := false
	syntax.Walk(node, func(child syntax.Node) bool {
		switch child.(type) {
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp,
			*syntax.ProcSubst, *syntax.ExtGlob, *syntax.BraceExp:
			dynamic = true
			return false
		default:
			return !dynamic
		}
	})
	return dynamic
}

func ruleForShellLeaf(tool string, leaf shellLeaf) Rule {
	if leaf.exact || leaf.call == nil || len(leaf.call.Args) == 0 {
		return shellExactRule(tool, leaf.text)
	}
	args := shellLeafArgs(leaf)
	if len(args) == 0 {
		return shellExactRule(tool, leaf.text)
	}
	return ruleFromTokens(tool, args, leaf.exact, leaf.text)
}

func shellLeafArgs(leaf shellLeaf) []string {
	if leaf.call == nil || len(leaf.call.Args) == 0 {
		return nil
	}
	args := make([]string, 0, len(leaf.call.Args))
	for _, word := range leaf.call.Args {
		value, ok := staticShellWord(word)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

func ruleFromTokens(tool string, args []string, exact bool, originalText string) Rule {
	if exact || len(args) == 0 {
		return shellExactRule(tool, originalText)
	}
	if exactShellCommands[args[0]] {
		return shellExactRule(tool, originalText)
	}
	prefixLen := shellPrefixLength(args)
	if prefixLen <= 0 {
		return shellExactRule(tool, originalText)
	}
	if prefixLen >= len(args) &&
		!subcommandShellCommands[args[0]] &&
		!flagPrefixShellCommands[args[0]] {
		return shellExactRule(tool, originalText)
	}
	return Rule{
		Type:    ShellPrefix,
		Tool:    tool,
		Pattern: strings.Join(args[:prefixLen], " ") + " *",
	}
}

func tokenizeSimple(s string) []string {
	var tokens []string
	var current strings.Builder
	inQuotes := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if ch == '"' {
			if inQuotes {
				tokens = append(tokens, current.String())
				current.Reset()
				inQuotes = false
			} else {
				if current.Len() > 0 {
					tokens = append(tokens, current.String())
					current.Reset()
				}
				inQuotes = true
			}
			continue
		}
		if !inQuotes && (ch == ' ' || ch == '\t') {
			if current.Len() > 0 {
				tokens = append(tokens, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteByte(ch)
	}
	if current.Len() > 0 {
		tokens = append(tokens, current.String())
	}
	return tokens
}

func splitSimpleCompound(s string) []string {
	var parts []string
	start := 0
	inQuotes := false
	for i := 0; i < len(s); i++ {
		if s[i] == '"' {
			inQuotes = !inQuotes
			continue
		}
		if inQuotes {
			continue
		}
		if i+1 < len(s) && s[i] == '&' && s[i+1] == '&' {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 2
			i++
			continue
		}
		if i+1 < len(s) && s[i] == '|' && s[i+1] == '|' {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 2
			i++
			continue
		}
		if s[i] == '&' && (i == 0 || s[i-1] != '>') {
			parts = append(parts, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(parts) == 0 {
		parts = append(parts, strings.TrimSpace(s[start:]))
	}
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

func commandHasPowerShellCompoundSyntax(s string) bool {
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\'':
			if !inDouble {
				inSingle = !inSingle
			}
		case '"':
			if !inSingle {
				inDouble = !inDouble
			}
		case ';', '|', '&', '{', '}', '\r', '\n':
			if !inSingle && !inDouble {
				return true
			}
		}
	}
	return false
}

func normalizeSimpleCommand(command string) string {
	return strings.TrimSpace(command)
}

func hasShellRedirection(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] == '>' {
			return true
		}
	}
	return false
}

func staticShellWord(word *syntax.Word) (string, bool) {
	var result strings.Builder
	for _, part := range word.Parts {
		switch value := part.(type) {
		case *syntax.Lit:
			result.WriteString(value.Value)
		case *syntax.SglQuoted:
			result.WriteString(value.Value)
		case *syntax.DblQuoted:
			for _, quotedPart := range value.Parts {
				lit, ok := quotedPart.(*syntax.Lit)
				if !ok {
					return "", false
				}
				result.WriteString(lit.Value)
			}
		default:
			return "", false
		}
	}
	return result.String(), true
}

var exactShellCommands = map[string]bool{
	"awk": true, "bash": true, "dash": true, "doas": true, "env": true,
	"eval": true, "exec": true, "find": true, "flock": true, "ionice": true,
	"ksh": true, "node": true, "perl": true, "python": true, "python3": true,
	"ruby": true, "sed": true, "setsid": true, "sh": true, "sudo": true,
	"tee": true, "watch": true, "xargs": true, "zsh": true,
}

var subcommandShellCommands = map[string]bool{
	"bun": true, "cargo": true, "docker": true, "gh": true, "git": true,
	"go": true, "helm": true, "kubectl": true, "npm": true, "pnpm": true,
	"yarn": true,
}

var flagPrefixShellCommands = map[string]bool{
	"chmod": true, "chown": true, "cp": true, "mkdir": true, "mv": true,
	"rm": true, "rmdir": true, "touch": true,
}

func shellToolUsesPOSIX(tool string) bool {
	if tool == "powershell_run" {
		return false
	}
	return runtime.GOOS != "windows"
}

func shellPrefixLength(args []string) int {
	if len(args) < 2 {
		return len(args)
	}
	command := args[0]
	if subcommandShellCommands[command] {
		if strings.HasPrefix(args[1], "-") {
			return 0
		}
		index := 2
		if (command == "npm" || command == "pnpm" || command == "yarn" || command == "bun") &&
			index < len(args) && args[index-1] == "run" {
			index++
		}
		return index
	}
	if flagPrefixShellCommands[command] {
		index := 1
		for index < len(args) && strings.HasPrefix(args[index], "-") {
			index++
		}
		return index
	}
	return 1
}

func shellExactRule(tool, command string) Rule {
	return Rule{
		Type:    ShellExact,
		Tool:    tool,
		Pattern: command,
	}
}

func dirAllowRulesForTool(tool string, command string, posix bool) []Rule {
	paths := extractCommandPaths(command, posix)
	if len(paths) == 0 {
		return nil
	}
	return []Rule{{
		Type:  DirAllow,
		Paths: paths,
	}}
}

func dirAllowForCommand(tool string, command string, rules []Rule, workspaceRoot string, posix bool) bool {
	targets := extractCommandPaths(command, posix)
	if len(targets) == 0 {
		return false
	}
	for _, rule := range rules {
		if rule.Type != DirAllow {
			continue
		}
		allMatch := true
		for _, target := range targets {
			if !dirWithinPaths(rule.Paths, target) {
				allMatch = false
				break
			}
		}
		if allMatch {
			return true
		}
	}
	return false
}

func extractCommandPaths(command string, posix bool) []string {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if !posix {
		return extractCommandPathsSimple(normalized)
	}
	return extractCommandPathsPOSIX(command)
}

func extractCommandPathsPOSIX(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var paths []string
	for _, stmt := range file.Stmts {
		collectPathsFromStmt(stmt, &paths)
	}
	if len(paths) == 0 {
		return nil
	}
	return dedupeSorted(paths)
}

func collectPathsFromStmt(stmt *syntax.Stmt, paths *[]string) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		collectPathsFromStmt(binary.X, paths)
		collectPathsFromStmt(binary.Y, paths)
		return
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) < 2 {
		return
	}
	hasDynamic := false
	for _, word := range call.Args {
		if !wordIsStatic(word) {
			hasDynamic = true
			break
		}
	}
	if hasDynamic {
		return
	}
	for _, word := range call.Args[1:] {
		value, ok := staticShellWord(word)
		if !ok {
			continue
		}
		if strings.HasPrefix(value, "-") {
			continue
		}
		if value == "" {
			continue
		}
		*paths = append(*paths, value)
	}
}

func wordIsStatic(word *syntax.Word) bool {
	for _, part := range word.Parts {
		switch part.(type) {
		case *syntax.Lit, *syntax.SglQuoted, *syntax.DblQuoted:
			continue
		default:
			return false
		}
	}
	return true
}

func extractCommandPathsSimple(command string) []string {
	normalized := normalizeSimpleCommand(command)
	if normalized == "" {
		return nil
	}
	parts := splitSimpleCompound(normalized)
	if len(parts) == 0 {
		return nil
	}
	var paths []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || hasShellRedirection(part) {
			continue
		}
		tokens := tokenizeSimple(part)
		if len(tokens) < 2 {
			continue
		}
		for _, token := range tokens[1:] {
			if strings.HasPrefix(token, "-") {
				continue
			}
			if token == "" {
				continue
			}
			paths = append(paths, token)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return dedupeSorted(paths)
}

func dirWithinPaths(allowed []string, target string) bool {
	for _, dir := range allowed {
		if target == dir || strings.HasPrefix(target, dir) {
			return true
		}
	}
	return false
}

func dedupeSorted(items []string) []string {
	sort.Strings(items)
	result := items[:0]
	for i, item := range items {
		if i == 0 || items[i-1] != item {
			result = append(result, item)
		}
	}
	return result
}
