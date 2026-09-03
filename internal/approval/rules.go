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
	RemoteAllow RuleType = "remote_allow"

	ScopeWorkspace ScopeKind = "workspace"
)

// Scope carries the current tool working directory for relative-path
// resolution and runtime path classification. It is not an authorization
// boundary for DirAllow or RemoteAllow rules.
type Scope struct {
	Kind  ScopeKind
	Value string
}

// Rule is a permission granted through the review UI. ScopeKind and ScopeValue
// remain only for loading older rule types and database rows; directory and
// remote write rules are task-scoped and are created without either field.
//
// Mode must be AccessWrite for DirAllow and RemoteAllow rules. Historical
// empty or read modes are loaded but intentionally do not authorize writes.
type Rule struct {
	ScopeKind  ScopeKind   `db:"scope_kind"`
	ScopeValue string      `db:"scope_value"`
	Type       RuleType    `db:"rule_type"`
	Tool       string      `db:"tool_name"`
	Pattern    string      `db:"pattern"`
	Paths      []string    `db:"paths"`
	Origins    []string    `db:"origins"`
	Mode       AccessClass `db:"mode"`
}

func WorkspaceScope(root string) Scope {
	return Scope{
		Kind:  ScopeWorkspace,
		Value: filepath.Clean(root),
	}
}

func (r Rule) key() string {
	pathsKey := strings.Join(r.Paths, "\x01")
	originsKey := strings.Join(r.Origins, "\x01")
	return string(r.ScopeKind) + "\x00" + r.ScopeValue + "\x00" +
		string(r.Type) + "\x00" + r.Tool + "\x00" + r.Pattern + "\x00" + pathsKey + "\x00" + originsKey + "\x00" + string(r.Mode)
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
		if r.Mode == "" {
			return fmt.Sprintf("dirs: %s", strings.Join(r.Paths, ", "))
		}
		return fmt.Sprintf("%s dirs: %s", r.Mode, strings.Join(r.Paths, ", "))
	case RemoteAllow:
		return fmt.Sprintf("remote writes: %s", strings.Join(r.Origins, ", "))
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
		if rule.Tool == "" && rule.Type != DirAllow && rule.Type != RemoteAllow {
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
