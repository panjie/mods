package approval

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
)

// Public matching predicates and the shell-rule bridge. These are the
// entry points the rest of the codebase calls (RuleSet.Allows,
// RulesFor, RulesForDirs, RulesAllowDirs, ShellRules*, ShellAllow*,
// ExtractShellCommand). They depend on rules.go for the data model,
// shell_parse.go for POSIX parsing, simple_tokenize.go for the
// fallback tokenizer, and writable_dirs.go for directory extraction.

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

// ExtractShellCommand decodes the JSON arguments of a shell tool call
// and returns the embedded "command" string. Returns "" on any parse
// failure, which the callers treat as "do not allow".
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

// matchShellPrefix reports whether command is exactly base or starts
// with base+" ". The word-boundary check prevents a "ls *" rule from
// matching "lsof".
func matchShellPrefix(pattern, command string) bool {
	base := strings.TrimSuffix(pattern, " *")
	return command == base || strings.HasPrefix(command, base+" ")
}

func dirAllowForCommand(tool string, command string, rules []Rule, _ string, posix bool) bool {
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

// dirWithinPaths reports whether target falls inside any of the
// allowed directories. It performs case-insensitive comparison for
// Windows-style paths and rejects sibling-prefix matches such as
// /tmp/cache2/file against an allowed /tmp/cache.
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
