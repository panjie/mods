package self

import (
	"regexp"
	"strings"
)

// FilesystemPathPattern detects prompt text that looks like a file path. It is
// a capability-exposure heuristic: when the prompt mentions files, filesystem
// tools may auto-enable. Editing it only changes WHEN tools arm, never whether
// a write is approved — that stays a kernel decision (review in normal mode,
// self-layer-only authorization in auto-evolution mode).
var FilesystemPathPattern = regexp.MustCompile(`(?i)(^|\s)(\.?/[\w.-]+|[\w.-]+/[\w./-]+|[\w.-]+\.(go|ts|tsx|js|jsx|py|rs|java|c|cc|cpp|h|hpp|md|txt|json|yaml|yml|toml|mod|sum|sh|sql))($|\s|[,.，。:：;；])`)

// FileRelatedKeywords make a prompt look filesystem-related. Evolvable: eva may
// extend this list (e.g. domain-specific terms) so filesystem tools arm for the
// prompts it cares about.
var FileRelatedKeywords = []string{
	"file", "files", "directory", "folder", "repo", "repository",
	"codebase", "source", "write", "edit", "modify", "patch",
	"grep", "rg",
	"文件", "目录", "代码", "仓库", "项目",
	"修改", "编辑", "修复",
}

// PromptLooksFileRelated reports whether a prompt suggests filesystem work, so
// filesystem tools can auto-enable. This is a capability hint, not a safety
// check: it never approves writes. Safety classifiers — the shell-mutable regex
// (internal/app) and the approval engine (internal/approval) — deliberately
// stay in the kernel and are NOT part of the self layer; weakening them would
// bypass the authorization gate, so they must remain immutable by evolution.
func PromptLooksFileRelated(prompt string) bool {
	p := strings.ToLower(prompt)
	for _, keyword := range FileRelatedKeywords {
		if strings.Contains(p, keyword) {
			return true
		}
	}
	return FilesystemPathPattern.MatchString(prompt)
}
