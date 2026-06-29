package approval

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"
)

// Rule types and the thread-safe RuleSet that stores them. These are
// the public data model of the approval package; matching predicates
// live in matching.go and shell parsing helpers live in shell_parse.go.

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

func shellExactRule(tool, command string) Rule {
	return Rule{
		Type:    ShellExact,
		Tool:    tool,
		Pattern: command,
	}
}
