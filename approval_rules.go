package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"

	"mvdan.cc/sh/v3/syntax"
)

type approvalRuleType string

const (
	approvalShellPrefix approvalRuleType = "shell_prefix"
	approvalShellExact  approvalRuleType = "shell_exact"
	approvalEditAll     approvalRuleType = "edit_all"
	approvalToolAll     approvalRuleType = "tool_all"
)

// ApprovalRule is a conversation-scoped permission granted through the review UI.
type ApprovalRule struct {
	Type    approvalRuleType `db:"rule_type"`
	Tool    string           `db:"tool_name"`
	Pattern string           `db:"pattern"`
}

func (r ApprovalRule) key() string {
	return string(r.Type) + "\x00" + r.Tool + "\x00" + r.Pattern
}

func (r ApprovalRule) String() string {
	switch r.Type {
	case approvalShellPrefix, approvalShellExact:
		return fmt.Sprintf("%s(%s)", r.Tool, r.Pattern)
	case approvalEditAll:
		return "file edits"
	case approvalToolAll:
		return r.Tool
	default:
		return r.Tool
	}
}

type approvalRuleSet struct {
	mu    sync.RWMutex
	rules []ApprovalRule
}

func (s *approvalRuleSet) replace(rules []ApprovalRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = dedupeApprovalRules(rules)
}

func (s *approvalRuleSet) add(rules ...ApprovalRule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = dedupeApprovalRules(append(s.rules, rules...))
}

func (s *approvalRuleSet) snapshot() []ApprovalRule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]ApprovalRule(nil), s.rules...)
}

func (s *approvalRuleSet) allows(name string, data []byte) bool {
	rules := s.snapshot()
	switch name {
	case "fs_write_file", "fs_apply_patch":
		return slices.ContainsFunc(rules, func(rule ApprovalRule) bool {
			return rule.Type == approvalEditAll
		})
	case "shell_run":
		command := extractShellCommand(data)
		if command == "" {
			return false
		}
		return shellRulesAllow(command, rules)
	default:
		return slices.ContainsFunc(rules, func(rule ApprovalRule) bool {
			return rule.Type == approvalToolAll && rule.Tool == name
		})
	}
}

func dedupeApprovalRules(rules []ApprovalRule) []ApprovalRule {
	seen := make(map[string]struct{}, len(rules))
	result := make([]ApprovalRule, 0, len(rules))
	for _, rule := range rules {
		if rule.Tool == "" {
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

func approvalRulesFor(name string, data []byte) []ApprovalRule {
	switch name {
	case "fs_write_file", "fs_apply_patch":
		return []ApprovalRule{{
			Type: approvalEditAll,
			Tool: "file_edit",
		}}
	case "shell_run":
		return shellApprovalRules(extractShellCommand(data))
	default:
		return []ApprovalRule{{
			Type: approvalToolAll,
			Tool: name,
		}}
	}
}

func approvalRulesLabel(rules []ApprovalRule) string {
	if len(rules) == 0 {
		return "this operation"
	}
	labels := make([]string, 0, len(rules))
	for _, rule := range rules {
		labels = append(labels, rule.String())
	}
	return strings.Join(labels, ", ")
}

func shellApprovalRules(command string) []ApprovalRule {
	return shellApprovalRulesWithMode(command, isUnixShell())
}

func shellApprovalRulesWithMode(command string, posix bool) []ApprovalRule {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return nil
	}
	if !posix {
		return shellApprovalRulesSimple(normalized)
	}
	leaves, ok := parseShellLeaves(command)
	if !ok || len(leaves) == 0 || len(leaves) > 5 {
		return []ApprovalRule{shellExactRule(normalized)}
	}
	rules := make([]ApprovalRule, 0, len(leaves))
	for _, leaf := range leaves {
		rules = append(rules, ruleForShellLeaf(leaf))
	}
	return dedupeApprovalRules(rules)
}

func shellApprovalRulesSimple(normalized string) []ApprovalRule {
	parts := splitSimpleCompound(normalized)
	if len(parts) == 0 || len(parts) > 5 {
		return []ApprovalRule{shellExactRule(normalized)}
	}
	var rules []ApprovalRule
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if hasShellRedirection(part) {
			rules = append(rules, shellExactRule(part))
			continue
		}
		tokens := tokenizeSimple(part)
		if len(tokens) == 0 {
			continue
		}
		rules = append(rules, ruleFromTokens(tokens, false, part))
	}
	return dedupeApprovalRules(rules)
}

func shellRulesAllow(command string, rules []ApprovalRule) bool {
	return shellRulesAllowWithMode(command, rules, isUnixShell())
}

func shellRulesAllowWithMode(command string, rules []ApprovalRule, posix bool) bool {
	normalized := normalizeShellCommandWithMode(command, posix)
	if normalized == "" {
		return false
	}
	if slices.ContainsFunc(rules, func(rule ApprovalRule) bool {
		return rule.Type == approvalShellExact && rule.Tool == "shell_run" && rule.Pattern == normalized
	}) {
		return true
	}
	if !posix {
		return shellRulesAllowSimple(normalized, rules)
	}
	leaves, ok := parseShellLeaves(command)
	if !ok || len(leaves) == 0 {
		return false
	}
	for _, leaf := range leaves {
		if !slices.ContainsFunc(rules, func(rule ApprovalRule) bool {
			if rule.Tool != "shell_run" {
				return false
			}
			switch rule.Type {
			case approvalShellExact:
				return rule.Pattern == leaf.text
			case approvalShellPrefix:
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

func shellRulesAllowSimple(normalized string, rules []ApprovalRule) bool {
	parts := splitSimpleCompound(normalized)
	for _, part := range parts {
		commandText := strings.TrimSpace(part)
		if commandText == "" {
			return false
		}
		found := false
		for _, rule := range rules {
			if rule.Tool != "shell_run" {
				continue
			}
			switch rule.Type {
			case approvalShellExact:
				if rule.Pattern == commandText {
					found = true
				}
			case approvalShellPrefix:
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
	return normalizeShellCommandWithMode(command, isUnixShell())
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

func ruleForShellLeaf(leaf shellLeaf) ApprovalRule {
	if leaf.exact || leaf.call == nil || len(leaf.call.Args) == 0 {
		return shellExactRule(leaf.text)
	}
	args := make([]string, 0, len(leaf.call.Args))
	for _, word := range leaf.call.Args {
		value, ok := staticShellWord(word)
		if !ok {
			return shellExactRule(leaf.text)
		}
		args = append(args, value)
	}
	return ruleFromTokens(args, leaf.exact, leaf.text)
}

func ruleFromTokens(args []string, exact bool, originalText string) ApprovalRule {
	if exact || len(args) == 0 {
		return shellExactRule(originalText)
	}
	if exactShellCommands[args[0]] {
		return shellExactRule(originalText)
	}
	prefixLen := shellPrefixLength(args)
	if prefixLen <= 0 {
		return shellExactRule(originalText)
	}
	if prefixLen >= len(args) &&
		!subcommandShellCommands[args[0]] &&
		!flagPrefixShellCommands[args[0]] {
		return shellExactRule(originalText)
	}
	return ApprovalRule{
		Type:    approvalShellPrefix,
		Tool:    "shell_run",
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
		if s[i] == '&' && (i == 0 || s[i-1] == ' ') {
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

var unixShellBases = map[string]bool{
	"bash": true, "sh": true, "zsh": true, "dash": true,
	"fish": true, "ksh": true, "tcsh": true, "csh": true,
}

func isUnixShell() bool {
	if s := os.Getenv("SHELL"); s != "" {
		base := strings.ToLower(filepath.Base(s))
		if unixShellBases[base] {
			return true
		}
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

func shellExactRule(command string) ApprovalRule {
	return ApprovalRule{
		Type:    approvalShellExact,
		Tool:    "shell_run",
		Pattern: command,
	}
}
