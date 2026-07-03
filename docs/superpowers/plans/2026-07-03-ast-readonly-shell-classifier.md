# AST 只读 Shell 命令分类器实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 利用 mvdan.cc/sh AST 在本地确定性判定 POSIX shell 命令是否只读，减少 LLM 分类器往返。

**Architecture:** 新建 `internal/approval/readonly.go`（纯逻辑 AST 遍历 + 命令表），集成到 `internal/app/shell_classify.go` 的 `analyzeShellCommand` 作为 tier 1 快速路径，位于现有 `isSimpleReadOnly`（tier 2）和 LLM 分类器（tier 3）之前。

**Tech Stack:** Go 1.25, mvdan.cc/sh/v3（已有依赖），syntax.Walk AST 遍历

## Global Constraints

- fail-closed：任何不确定情况返回 `false`，降级到 LLM
- 不引入新依赖
- 不修改 `review.go`、`request_session.go`（远端已重构，改进 `analyzeShellCommand` 后效果自动传递）
- `powershell_run` 跳过 AST 分类器
- 复用 `internal/approval/shell_parse.go` 的 `staticShellWord` 和 `redirectionWrites`
- 验证：`go test ./internal/approval -run TestIsReadOnlyPOSIX -count=1` 和 `go test ./internal/app -run TestAnalyzeShellCommand -count=1` 和 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`

---

### Task 1: AST 只读分类器 `internal/approval/readonly.go`

**Files:**
- Create: `internal/approval/readonly.go`
- Test: `internal/approval/readonly_test.go`

**Interfaces:**
- Consumes: `staticShellWord(*syntax.Word) (string, bool)` from `shell_parse.go:158`, `redirectionWrites(syntax.RedirOperator) bool` from `shell_parse.go:193`
- Produces: `IsReadOnlyPOSIX(command string) (bool, string)` — 返回 `(true, reason)` 表示只读；`(false, "")` 表示非只读或无法确定

- [ ] **Step 1: Write the test file `internal/approval/readonly_test.go`**

```go
package approval

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsReadOnlyPOSIX(t *testing.T) {
	cases := []struct {
		name string
		cmd  string
		want bool
	}{
		// --- Simple allowlist commands ---
		{"simple ls", "ls", true},
		{"ls with flags", "ls -la", true},
		{"cat file", "cat README.md", true},
		{"diff two files", "diff a b", true},
		{"tr transform", "tr a-z A-Z", true},
		{"printf", `printf "hello"`, true},
		{"test builtin", "test -f x", true},
		{"bracket test", "[ -f x ]", true},
		{"true", "true", true},
		{"false", "false", true},
		{"seq", "seq 1 10", true},
		{"md5sum", "md5sum file", true},
		{"full path", "/bin/ls", true},
		{"full path with arg", "/usr/bin/cat file", true},

		// --- Pipes (all leaves read-only) ---
		{"pipe cat grep", "cat file | grep foo", true},
		{"pipe ls head", "ls -la | head -5", true},
		{"pipe echo tr", "echo hello | tr a-z A-Z", true},
		{"triple pipe", "cat file | grep foo | head -5", true},

		// --- Binary && / || (both sides read-only) ---
		{"and git", "git status && git diff", true},
		{"or true false", "true || false", true},
		{"and test echo", "test -f x && echo y", true},

		// --- Subcommands ---
		{"git status", "git status", true},
		{"git log", "git log --oneline", true},
		{"git diff", "git diff", true},
		{"git show", "git show HEAD", true},
		{"git blame", "git blame file.go", true},
		{"docker ps", "docker ps -a", true},
		{"docker logs", "docker logs container", true},
		{"kubectl get", "kubectl get pods", true},
		{"kubectl describe", "kubectl describe pod x", true},
		{"go version", "go version", true},
		{"go list", "go list ./...", true},
		{"npm list", "npm list", true},
		{"pnpm ls", "pnpm ls", true},

		// --- Subshells ---
		{"subshell git", "(git status)", true},
		{"subshell multi", "(ls -la; cat file)", true},

		// --- Command substitution (inner read-only) ---
		{"cmdsubst date", "echo $(date)", true},
		{"cmdsubst git", "echo $(git status)", true},
		{"cmdsubst git ls-files", "cat $(git ls-files)", true},

		// --- Input redirect ---
		{"input redirect", "tr a-z A-Z < input", true},

		// --- ParamExp ---
		{"param exp", "echo $VAR", true},
		{"param exp braced", "cat ${FILE}", true},

		// --- Multiple statements ---
		{"multi stmt", "git status; git diff", true},

		// --- NOT read-only ---
		{"output redirect", "echo hello > file.txt", false},
		{"append redirect", "ls >> out.log", false},
		{"pipe with tee", "cat file | tee output", false},
		{"rm", "rm file", false},
		{"find", "find . -name '*.go'", false},
		{"sort", "sort file", false},
		{"make", "make", false},
		{"git push", "git push", false},
		{"git commit", "git commit -m msg", false},
		{"docker run", "docker run img", false},
		{"cmdsubst with rm", "echo $(rm file)", false},
		{"procsubst", "diff <(cmd1) <(cmd2)", false},
		{"if clause", "if true; then ls; fi", false},
		{"for loop", "for f in *.go; do echo $f; done", false},
		{"background", "ls &", false},
		{"dynamic cmd name", "$CMD file", false},
		{"empty", "", false},
		{"bare git", "git", false},
		{"bare docker", "docker", false},
		{"git global flag", "git --git-dir=/x status", false},
		{"unknown subcmd", "git push", false},
		{"sed", "sed 's/a/b/' file", false},
		{"awk", "awk '{print $1}'", false},
		{"curl", "curl https://example.com", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := IsReadOnlyPOSIX(c.cmd)
			require.Equalf(t, c.want, got, "cmd=%q", c.cmd)
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/approval -run TestIsReadOnlyPOSIX -count=1`
Expected: FAIL with "undefined: IsReadOnlyPOSIX"

- [ ] **Step 3: Write implementation `internal/approval/readonly.go`**

```go
package approval

import (
	"path"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// IsReadOnlyPOSIX analyzes a POSIX shell command using the mvdan.cc/sh AST
// and reports whether it is definitively read-only. Returns (true, reason)
// when read-only; (false, "") when not or inconclusive (fail-closed).
func IsReadOnlyPOSIX(command string) (bool, string) {
	command = strings.TrimSpace(command)
	if command == "" {
		return false, ""
	}
	parser := syntax.NewParser(syntax.Variant(syntax.LangPOSIX))
	file, err := parser.Parse(strings.NewReader(command), "")
	if err != nil {
		return false, ""
	}
	for _, stmt := range file.Stmts {
		if ro, _ := stmtIsReadOnly(stmt); !ro {
			return false, ""
		}
	}
	return true, "read-only command (AST analysis)"
}

// stmtIsReadOnly checks a single statement: background, redirects, then
// delegates to the command-level check.
func stmtIsReadOnly(stmt *syntax.Stmt) (bool, string) {
	if stmt == nil || stmt.Cmd == nil {
		return false, ""
	}
	if stmt.Background {
		return false, ""
	}
	for _, redir := range stmt.Redirs {
		if redir == nil {
			continue
		}
		if redirectionWrites(redir.Op) {
			return false, ""
		}
		if redir.Word != nil && wordHasProcSubst(redir.Word) {
			return false, ""
		}
	}
	return cmdIsReadOnly(stmt.Cmd)
}

// cmdIsReadOnly dispatches on command type. BinaryCmd and Subshell recurse;
// CallExpr does leaf classification; everything else is fail-closed.
func cmdIsReadOnly(cmd syntax.Command) (bool, string) {
	switch c := cmd.(type) {
	case *syntax.BinaryCmd:
		if ro, _ := stmtIsReadOnly(c.X); !ro {
			return false, ""
		}
		return stmtIsReadOnly(c.Y)
	case *syntax.Subshell:
		for _, stmt := range c.Stmts {
			if ro, _ := stmtIsReadOnly(stmt); !ro {
				return false, ""
			}
		}
		return true, "read-only subshell"
	case *syntax.CallExpr:
		return callIsReadOnly(c)
	default:
		return false, ""
	}
}

// callIsReadOnly classifies a leaf command: checks word parts for dynamic
// constructs, extracts the command name, then checks the allowlist or
// subcommand table.
func callIsReadOnly(call *syntax.CallExpr) (bool, string) {
	if call == nil || len(call.Args) == 0 {
		return false, ""
	}
	for _, arg := range call.Args {
		if !wordIsReadOnly(arg) {
			return false, ""
		}
	}
	name, ok := staticShellWord(call.Args[0])
	if !ok || name == "" {
		return false, ""
	}
	name = path.Base(name)

	if readOnlyCommands[name] {
		return true, "read-only command: " + name
	}
	if subcommands, ok := subcommandReadOnly[name]; ok {
		if len(call.Args) < 2 {
			return false, ""
		}
		subcmd, ok := staticShellWord(call.Args[1])
		if !ok || subcmd == "" {
			return false, ""
		}
		if strings.HasPrefix(subcmd, "-") {
			return false, ""
		}
		if subcommands[subcmd] {
			return true, "read-only subcommand: " + name + " " + subcmd
		}
		return false, ""
	}
	return false, ""
}

// wordIsReadOnly walks a word's parts. CmdSubst recurses into inner
// statements; ProcSubst is fail-closed; everything else is allowed.
func wordIsReadOnly(word *syntax.Word) bool {
	if word == nil {
		return true
	}
	readonly := true
	syntax.Walk(word, func(node syntax.Node) bool {
		if !readonly {
			return false
		}
		switch n := node.(type) {
		case *syntax.ProcSubst:
			readonly = false
			return false
		case *syntax.CmdSubst:
			if !stmtsAreReadOnly(n.Stmts) {
				readonly = false
				return false
			}
			return false
		default:
			return true
		}
	})
	return readonly
}

// wordHasProcSubst reports whether a word contains any ProcSubst node.
func wordHasProcSubst(word *syntax.Word) bool {
	if word == nil {
		return false
	}
	found := false
	syntax.Walk(word, func(node syntax.Node) bool {
		if found {
			return false
		}
		if _, ok := node.(*syntax.ProcSubst); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

func stmtsAreReadOnly(stmts []*syntax.Stmt) bool {
	for _, stmt := range stmts {
		if ro, _ := stmtIsReadOnly(stmt); !ro {
			return false
		}
	}
	return true
}

// readOnlyCommands are always read-only regardless of flags.
var readOnlyCommands = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true,
	"wc": true, "file": true, "stat": true, "pwd": true,
	"echo": true, "date": true, "whoami": true, "hostname": true,
	"uname": true, "du": true, "df": true, "which": true,
	"env": true, "printenv": true, "basename": true, "dirname": true,
	"realpath": true, "readlink": true,
	"grep": true, "egrep": true, "fgrep": true,
	"diff": true, "uniq": true, "comm": true, "tr": true,
	"cut": true, "strings": true, "xxd": true, "od": true,
	"hexdump": true, "nm": true, "objdump": true, "readelf": true,
	"md5sum": true, "sha1sum": true, "sha256sum": true, "sha512sum": true,
	"shasum": true, "cksum": true, "test": true, "[": true,
	"true": true, "false": true, "seq": true, "printf": true,
	"id": true, "groups": true, "lsof": true, "ps": true,
	"free": true, "uptime": true, "w": true, "column": true,
	"paste": true, "expand": true, "unexpand": true, "nl": true,
	"rev": true, "tac": true, "fold": true, "fmt": true,
	"join": true,
}

// subcommandReadOnly maps a tool to its read-only subcommands.
var subcommandReadOnly = map[string]map[string]bool{
	"git": {
		"status": true, "log": true, "diff": true, "show": true,
		"blame": true, "annotate": true, "rev-parse": true, "describe": true,
		"reflog": true, "shortlog": true, "ls-files": true, "ls-tree": true,
		"ls-remote": true, "whatchanged": true, "cat-file": true,
		"rev-list": true, "merge-base": true, "name-rev": true,
		"var": true, "for-each-ref": true,
	},
	"docker": {
		"ps": true, "images": true, "logs": true, "inspect": true,
		"stats": true, "top": true, "history": true, "search": true,
		"version": true, "info": true, "events": true,
	},
	"kubectl": {
		"get": true, "describe": true, "logs": true, "explain": true,
		"top": true, "version": true, "api-resources": true,
		"api-versions": true, "cluster-info": true, "diff": true,
	},
	"go": {
		"version": true, "list": true, "vet": true, "doc": true,
		"help": true,
	},
	"npm": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
	"pnpm": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
	"yarn": {
		"list": true, "ls": true, "outdated": true, "info": true,
		"view": true, "root": true, "help": true, "why": true,
		"explain": true,
	},
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/approval -run TestIsReadOnlyPOSIX -count=1`
Expected: PASS

- [ ] **Step 5: Run full approval package tests**

Run: `go test ./internal/approval -count=1`
Expected: PASS (no regressions in existing tests)

- [ ] **Step 6: Commit**

```bash
git add internal/approval/readonly.go internal/approval/readonly_test.go
git commit -m "feat(approval): add AST-based POSIX shell read-only classifier"
```

---

### Task 2: 集成到 `internal/app/shell_classify.go`

**Files:**
- Modify: `internal/app/shell_classify.go:37-79` (`analyzeShellCommand` function)
- Test: `internal/app/shell_classify_test.go`

**Interfaces:**
- Consumes: `IsReadOnlyPOSIX(command string) (bool, string)` from Task 1
- Produces: modified `analyzeShellCommand` that tries AST classifier before `isSimpleReadOnly`

- [ ] **Step 1: Add integration test cases to `internal/app/shell_classify_test.go`**

Append these test functions to the existing test file:

```go
func TestAnalyzeShellCommandASTReadOnly(t *testing.T) {
	// These commands would previously fall through to the LLM classifier
	// because of shell metacharacters (|, &&, $()) or missing from the
	// simple allowlist. The AST classifier should catch them locally.
	cases := []struct {
		name string
		tool string
		cmd  string
	}{
		{"pipe cat grep", "shell_run", "cat file | grep foo"},
		{"pipe ls head", "shell_run", "ls -la | head -5"},
		{"git status", "shell_run", "git status"},
		{"git log", "shell_run", "git log --oneline"},
		{"docker ps", "shell_run", "docker ps -a"},
		{"kubectl get", "shell_run", "kubectl get pods"},
		{"cmdsubst date", "shell_run", "echo $(date)"},
		{"and git", "shell_run", "git status && git diff"},
		{"subshell", "shell_run", "(git status)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			mods := &Mods{
				shellAnalyzer: func(tool, command string) shellCommandAnalysis {
					t.Fatalf("LLM classifier should not be called for %q", command)
					return defaultShellCommandAnalysis()
				},
			}
			t.Cleanup(func() { mods.shellAnalyzer = nil })
			result := mods.analyzeShellCommand(c.tool, c.cmd)
			require.Falsef(t, result.NeedsReview, "cmd=%q should be read-only", c.cmd)
			require.NotEmptyf(t, result.Reason, "cmd=%q should have a reason", c.cmd)
		})
	}
}

func TestAnalyzeShellCommandASTExternalPath(t *testing.T) {
	// Read-only command with external path: AST classifier says read-only,
	// extractExternalPaths provides AffectedDirs, no LLM call needed.
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			t.Fatalf("LLM classifier should not be called for %q", command)
			return defaultShellCommandAnalysis()
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	result := mods.analyzeShellCommand("shell_run", "cat /etc/passwd")
	require.False(t, result.NeedsReview, "cat /etc/passwd should be read-only")
	require.Contains(t, result.AffectedDirs, "/etc/passwd")
}

func TestAnalyzeShellCommandPowershellSkipsAST(t *testing.T) {
	// powershell_run should skip the AST classifier and use the test seam
	// (which simulates the LLM path).
	called := false
	mods := &Mods{
		shellAnalyzer: func(tool, command string) shellCommandAnalysis {
			called = true
			return shellCommandAnalysis{NeedsReview: true, Reason: "powershell"}
		},
	}
	t.Cleanup(func() { mods.shellAnalyzer = nil })
	mods.analyzeShellCommand("powershell_run", "Get-ChildItem")
	require.True(t, called, "shellAnalyzer should be called for powershell_run")
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/app -run TestAnalyzeShellCommandAST -count=1`
Expected: FAIL — the AST classifier is not yet integrated, so `shellAnalyzer` gets called and the test's `t.Fatalf` triggers.

- [ ] **Step 3: Modify `analyzeShellCommand` in `internal/app/shell_classify.go`**

Replace the fast-path section (lines 47-57) with the 3-tier pipeline. The old code:

```go
	// Local fast-path: conservative allowlist of read-only commands with no
	// shell metacharacters and no external-path references. Everything else,
	// including read-only commands that touch external paths, goes to the
	// LLM classifier for precise analysis.
	if isSimpleReadOnly(command) && len(extractExternalPaths(command, ws)) == 0 {
		debug.Printf("analyzeShellCommand: cmd=%q -> local: read-only, workspace-local", debug.Truncate(command, 80))
		return shellCommandAnalysis{
			NeedsReview: false,
			Reason:      "read-only command, workspace-local (local heuristic)",
		}
	}
```

Replace with:

```go
	externalPaths := extractExternalPaths(command, ws)

	// Tier 1: AST-based read-only classifier (POSIX only, skip powershell_run).
	// Handles pipes, &&/||, subshells, command substitution, and subcommand
	// tables. Covers both workspace-local and external-path read-only commands;
	// the approval matrix decides whether external reads need review.
	if tool != "powershell_run" {
		if ro, reason := approval.IsReadOnlyPOSIX(command); ro {
			debug.Printf("analyzeShellCommand: cmd=%q -> AST: read-only", debug.Truncate(command, 80))
			return shellCommandAnalysis{
				NeedsReview:  false,
				AffectedDirs: externalPaths,
				Reason:       reason,
			}
		}
	}

	// Tier 2: conservative allowlist fallback for commands the AST parser
	// can't handle (e.g. cmd.exe syntax on Windows). Only matches commands
	// with no shell metacharacters and no external-path references.
	if isSimpleReadOnly(command) && len(externalPaths) == 0 {
		debug.Printf("analyzeShellCommand: cmd=%q -> local: read-only, workspace-local", debug.Truncate(command, 80))
		return shellCommandAnalysis{
			NeedsReview: false,
			Reason:      "read-only command, workspace-local (local heuristic)",
		}
	}
```

Also add the import for the approval package at the top of the file. The existing imports include `"github.com/panjie/mods/internal/proto"` etc. Add:

```go
	"github.com/panjie/mods/internal/approval"
```

And update the post-process section to reuse `externalPaths` instead of calling `extractExternalPaths` again. The old code (lines 62-76):

```go
	// Post-process: merge regex-detected external paths into AffectedDirs
	// so external access is never silently dropped when the LLM omits dirs
	// (read-only commands) or fails entirely.
	for _, p := range extractExternalPaths(command, ws) {
```

Replace `extractExternalPaths(command, ws)` with `externalPaths`:

```go
	// Post-process: merge regex-detected external paths into AffectedDirs
	// so external access is never silently dropped when the LLM omits dirs
	// (read-only commands) or fails entirely.
	for _, p := range externalPaths {
```

- [ ] **Step 4: Run integration tests to verify they pass**

Run: `go test ./internal/app -run TestAnalyzeShellCommandAST -count=1`
Expected: PASS

- [ ] **Step 5: Run full app package tests**

Run: `go test ./internal/app -count=1`
Expected: PASS (no regressions)

- [ ] **Step 6: Run build check**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
Expected: PASS (`go build ./...` succeeds)

- [ ] **Step 7: Run full test suite**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 test`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/app/shell_classify.go internal/app/shell_classify_test.go
git commit -m "feat(app): integrate AST read-only classifier as tier 1 fast-path"
```
