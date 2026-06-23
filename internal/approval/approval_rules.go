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
		return nil
	default:
		return []Rule{{
			Type: ToolAll,
			Tool: name,
		}}
	}
}

func RulesForDirs(dirs []string, scope Scope) []Rule {
	if len(dirs) == 0 {
		return nil
	}
	return scopeRules([]Rule{{
		Type:  DirAllow,
		Paths: dirs,
	}}, scope)
}

func RulesAllowDirs(rules []Rule, dirs []string, scope Scope) bool {
	if len(dirs) == 0 {
		return false
	}
	scopedRules := rulesForScope(rules, scope)
	for _, rule := range scopedRules {
		if rule.Type != DirAllow {
			continue
		}
		allMatch := true
		for _, dir := range dirs {
			if !dirWithinPaths(rule.Paths, dir) {
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
	dirs := extractWritableDirs(command, posix)
	if len(dirs) == 0 {
		return nil
	}
	return []Rule{{
		Type:  DirAllow,
		Paths: dirs,
	}}
}

func dirAllowForCommand(tool string, command string, rules []Rule, workspaceRoot string, posix bool) bool {
	targetDirs := extractWritableDirs(command, posix)
	if len(targetDirs) == 0 {
		return false
	}
	for _, rule := range rules {
		if rule.Type != DirAllow {
			continue
		}
		allMatch := true
		for _, targetDir := range targetDirs {
			if !dirWithinPaths(rule.Paths, targetDir) {
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

func extractWritableDirs(command string, posix bool) []string {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if !posix {
		return extractWritableDirsSimple(normalized)
	}
	return extractWritableDirsPOSIX(command)
}

func extractWritableDirsPOSIX(command string) []string {
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return nil
	}
	var dirs []string
	for _, stmt := range file.Stmts {
		collectWritableDirsFromStmt(stmt, &dirs)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

func collectWritableDirsFromStmt(stmt *syntax.Stmt, dirs *[]string) {
	if stmt == nil || stmt.Cmd == nil {
		return
	}
	for _, redir := range stmt.Redirs {
		if redir == nil || redir.Word == nil || !redirectionWrites(redir.Op) {
			continue
		}
		target, ok := staticShellWord(redir.Word)
		if !ok || target == "" {
			continue
		}
		*dirs = append(*dirs, parentDir(target))
	}
	if binary, ok := stmt.Cmd.(*syntax.BinaryCmd); ok {
		collectWritableDirsFromStmt(binary.X, dirs)
		collectWritableDirsFromStmt(binary.Y, dirs)
		return
	}
	call, ok := stmt.Cmd.(*syntax.CallExpr)
	if !ok || len(call.Args) == 0 {
		return
	}
	args := shellWords(call.Args)
	if len(args) == 0 {
		return
	}
	*dirs = append(*dirs, writableDirsFromTokens(args, true)...)
}

func redirectionWrites(op syntax.RedirOperator) bool {
	switch op {
	case syntax.RdrOut, syntax.AppOut, syntax.ClbOut, syntax.RdrAll, syntax.AppAll, syntax.RdrInOut:
		return true
	default:
		return false
	}
}

func extractWritableDirsSimple(command string) []string {
	parts := splitSimpleCompound(normalizeSimpleCommand(command))
	if len(parts) == 0 {
		return nil
	}
	var dirs []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		dirs = append(dirs, writableDirsFromRedirection(part)...)
		tokens := tokenizeSimple(part)
		if len(tokens) == 0 {
			continue
		}
		dirs = append(dirs, writableDirsFromTokens(tokens, false)...)
	}
	if len(dirs) == 0 {
		return nil
	}
	return dedupeSorted(dirs)
}

func shellWords(words []*syntax.Word) []string {
	args := make([]string, 0, len(words))
	for _, word := range words {
		value, ok := staticShellWord(word)
		if !ok {
			return nil
		}
		args = append(args, value)
	}
	return args
}

func writableDirsFromTokens(args []string, posix bool) []string {
	if len(args) == 0 {
		return nil
	}
	command := args[0]
	if !posix {
		command = strings.ToLower(command)
	}
	switch command {
	case "rm", "rmdir", "unlink", "touch", "chmod", "chown":
		return parentDirs(commandOperands(args[1:]))
	case "mkdir":
		return parentDirs(commandOperands(args[1:]))
	case "cp", "mv":
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "tee":
		return parentDirs(commandOperands(args[1:]))
	case "remove-item", "del", "erase", "rd":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	case "copy-item", "move-item":
		if destinations := powerShellParamValues(args, "destination"); len(destinations) > 0 {
			return destinationDirs(destinations)
		}
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "copy", "move":
		operands := commandOperands(args[1:])
		if len(operands) == 0 {
			return nil
		}
		return []string{destinationDir(operands[len(operands)-1])}
	case "new-item", "set-content", "add-content":
		if paths := powerShellParamValues(args, "path", "literalpath"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	case "out-file":
		if paths := powerShellParamValues(args, "filepath", "literalpath", "path"); len(paths) > 0 {
			return parentDirs(paths)
		}
		return parentDirs(commandOperands(args[1:]))
	default:
		return nil
	}
}

func powerShellParamValues(args []string, names ...string) []string {
	nameSet := make(map[string]struct{}, len(names))
	for _, name := range names {
		nameSet[strings.ToLower(name)] = struct{}{}
	}
	var values []string
	for i := 1; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" {
			continue
		}
		key := strings.TrimLeft(arg, "-")
		var inlineValue string
		if before, after, ok := strings.Cut(key, ":"); ok {
			key = before
			inlineValue = after
		}
		if _, ok := nameSet[strings.ToLower(key)]; !ok {
			continue
		}
		if inlineValue != "" {
			values = append(values, inlineValue)
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			values = append(values, args[i+1])
			i++
		}
	}
	return values
}

func commandOperands(args []string) []string {
	operands := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" {
			continue
		}
		if arg == "--" {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		operands = append(operands, arg)
	}
	return operands
}

func parentDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, parentDir(path))
	}
	return dirs
}

func destinationDirs(paths []string) []string {
	dirs := make([]string, 0, len(paths))
	for _, path := range paths {
		dirs = append(dirs, destinationDir(path))
	}
	return dirs
}

func writableDirsFromRedirection(command string) []string {
	tokens := tokenizeSimple(command)
	dirs := make([]string, 0)
	for i, token := range tokens {
		if !isRedirectionToken(token) || i+1 >= len(tokens) {
			continue
		}
		dirs = append(dirs, parentDir(tokens[i+1]))
	}
	return dirs
}

func isRedirectionToken(token string) bool {
	return token == ">" || token == ">>" || token == "1>" || token == "1>>" || token == "2>" || token == "2>>"
}

func destinationDir(path string) string {
	if strings.HasSuffix(path, "/") || strings.HasSuffix(path, "\\") {
		return cleanDir(path)
	}
	return parentDir(path)
}

func parentDir(path string) string {
	path = cleanDir(path)
	if path == "" {
		return "."
	}
	if windowsStylePath(path) {
		if windowsDriveRoot(path) {
			return path
		}
		if i := strings.LastIndex(path, `\`); i >= 0 {
			if i == 0 {
				return path[:1]
			}
			if i == 2 && len(path) >= 2 && path[1] == ':' {
				return path[:i+1]
			}
			return strings.TrimRight(path[:i], `\`)
		}
		return "."
	}
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		if i == 0 {
			return path[:1]
		}
		if i == 2 && len(path) >= 2 && path[1] == ':' {
			return path[:i]
		}
		return strings.TrimRight(path[:i], `/\`)
	}
	return "."
}

func cleanDir(path string) string {
	path = strings.TrimSpace(path)
	if windowsStylePath(path) {
		return cleanWindowsPath(path)
	}
	cleaned := filepath.Clean(path)
	if cleaned == "" {
		return "."
	}
	return cleaned
}

func dirWithinPaths(allowed []string, target string) bool {
	for _, dir := range allowed {
		dir = cleanDir(dir)
		target = cleanDir(target)
		if dir == "." {
			if target == "." || !filepath.IsAbs(target) && !windowsPathIsAbs(target) {
				return true
			}
			continue
		}
		compareDir, compareTarget := dir, target
		if windowsStylePath(dir) || windowsStylePath(target) {
			compareDir = strings.ToLower(compareDir)
			compareTarget = strings.ToLower(compareTarget)
		}
		if compareTarget == compareDir || strings.HasPrefix(compareTarget, descendantPrefix(compareDir)) {
			return true
		}
	}
	return false
}

func descendantPrefix(path string) string {
	separator := pathSeparatorFor(path)
	if strings.HasSuffix(path, separator) {
		return path
	}
	return path + separator
}

func pathSeparatorFor(path string) string {
	if strings.Contains(path, "\\") {
		return "\\"
	}
	return "/"
}

func windowsPathIsAbs(path string) bool {
	return len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/')
}

func windowsStylePath(path string) bool {
	return strings.Contains(path, `\`) || windowsPathHasDrive(path)
}

func windowsPathHasDrive(path string) bool {
	return len(path) >= 2 && path[1] == ':'
}

func windowsDriveRoot(path string) bool {
	return len(path) == 3 && path[1] == ':' && path[2] == '\\'
}

func cleanWindowsPath(path string) string {
	path = strings.ReplaceAll(path, "/", `\`)
	if path == "" {
		return "."
	}
	for len(path) > 1 && strings.HasSuffix(path, `\`) && !windowsDriveRoot(path) {
		path = strings.TrimSuffix(path, `\`)
	}
	return path
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
