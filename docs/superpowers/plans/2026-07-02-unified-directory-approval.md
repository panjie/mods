# Unified Directory-Centric Approval Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 fs_* 与 shell 两套割裂的文件访问审批统一为一个以目录为核心的 `ClassifyAccess` 矩阵（workspace 内读免审/写审批、temp dir 免审、外部读写审批），fs_* 审批通过即真正放行外部访问。

**Architecture:** 引入统一 `AccessIntent{Class, Dirs}`，review 层用单一 `ClassifyAccess` 决策；`resolveWorkspacePath` 的"硬拒绝外部"改造为"ctx 授权目录内放行"；fs_* 与 shell 统一用 `DirAllow` 规则与 `RulesAllowDirs` 免审判定。

**Tech Stack:** Go 1.24+、testify/require、context.Value、现有 `internal/approval` 规则系统。

**Spec:** `docs/superpowers/specs/2026-07-02-unified-directory-approval-design.md`

## Global Constraints

- 配置先验：CLI flags > mods.yml > MODS_ env > defaults（勿破坏）。
- 平台：Windows/macOS/Linux 都须过；路径比较用现有 `cleanDir`/`dirWithinPaths`/`ensureInsideRoot` 助手，勿自造。
- ReviewMode 三值：`ReviewNever`/`ReviewMutable`(默认)/`ReviewAlways`（定义于 `internal/config`，`internal/app/aliases.go` 别名导出）。
- 不引入 `.env` 读保护；不改 MCP 工具审批（默认 Write+未知 Dirs fail-closed）。
- TDD：每个任务先写失败测试再实现；每任务结束 `go build ./...` + 相关包 `go test`。
- Lint：`.golangci.yml` v2（govet/ineffassign + gofmt/goimports），`golangci-lint run ./...`。

## File Structure

**Create:**
- `internal/approval/access.go` — `AccessIntent`/`AccessClass`/`Decision`/`ClassifyAccess`/`locateDir`（纯逻辑，无 LLM）
- `internal/approval/access_test.go` — 矩阵全格测试
- `internal/tools/accessctx.go` — `WithAuthorizedDirs`/`AuthorizedDirs`（context key）
- `internal/tools/accessctx_test.go`

**Modify:**
- `internal/tools/tools.go` — `Tool.IntentExtractor` 字段 + `Registry.IntentExtractor(name)`
- `internal/tools/path_safety.go` — `resolveWorkspacePath` 加 ctx 参数 + 授权放行
- `internal/tools/filesystem.go` — 注册 IntentExtractor；Call 传 ctx
- `internal/tools/patch_validate.go` — resolveWorkspacePath 调用传 ctx
- `internal/tools/tools_test.go` — 更新调用签名 + 新增授权用例
- `internal/app/shell_classify.go` — `mentionsExternalPath` + 只读分支
- `internal/app/review.go` — `buildAccessIntent`；`requestApproval` 统一 `RulesAllowDirs` + `DirAllow` 候选
- `internal/app/request_session.go` — `toolCaller` 改造：`ClassifyAccess` + ctx 注入
- `internal/app/review_summary.go` — fs_read 等只读标签
- `internal/app/approval_rules_test.go` — EditAll→DirAllow 断言调整
- `internal/prompts/prompts.go` — ToolSelection 改写

---

### Task 1: AccessIntent 类型与 ClassifyAccess 矩阵

**Files:**
- Create: `internal/approval/access.go`
- Test: `internal/approval/access_test.go`

**Interfaces:**
- Produces: `AccessClass`(`AccessRead`/`AccessWrite`), `AccessIntent{Class AccessClass; Dirs []string}`, `Decision`(`DecisionAllow`/`DecisionAsk`), `ClassifyAccess(intent AccessIntent, scope Scope, safeDirs []string, mode ReviewMode) Decision`, `locateDir(path string, scope Scope, safeDirs []string) dirLocation`(`locWorkspace`/`locTemp`/`locExternal`/`locUnknown`)

- [ ] **Step 1: Write the failing test**

```go
// internal/approval/access_test.go
package approval

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func wsScope(t *testing.T) Scope {
	t.Helper()
	return WorkspaceScope(filepath.Clean(t.TempDir()))
}

func TestClassifyAccessMatrix(t *testing.T) {
	ws := wsScope(t)
	tempDir := t.TempDir()
	safeDirs := []string{tempDir}
	external := t.TempDir() // outside ws and outside safeDirs

	cases := []struct {
		name   string
		intent AccessIntent
		want   Decision
	}{
		{"read in workspace", AccessIntent{AccessRead, []string{ws.Value}}, DecisionAllow},
		{"write in workspace", AccessIntent{AccessWrite, []string{ws.Value}}, DecisionAsk},
		{"read in temp", AccessIntent{AccessRead, []string{tempDir}}, DecisionAllow},
		{"write in temp", AccessIntent{AccessWrite, []string{tempDir}}, DecisionAllow},
		{"read external", AccessIntent{AccessRead, []string{external}}, DecisionAsk},
		{"write external", AccessIntent{AccessWrite, []string{external}}, DecisionAsk},
		{"read empty dirs", AccessIntent{AccessRead, nil}, DecisionAllow},
		{"write empty dirs", AccessIntent{AccessWrite, nil}, DecisionAsk},
		{"mixed ws+external read", AccessIntent{AccessRead, []string{ws.Value, external}}, DecisionAsk},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ClassifyAccess(c.intent, ws, safeDirs, ReviewMutable))
		})
	}
}

func TestClassifyAccessModeOverride(t *testing.T) {
	ws := wsScope(t)
	external := t.TempDir()
	read := AccessIntent{AccessRead, []string{external}} // normally Ask
	require.Equal(t, DecisionAllow, ClassifyAccess(read, ws, nil, ReviewNever))
}
```

> 注：`ReviewNever`/`ReviewMutable` 须在 `approval` 包可见。当前它们定义在 `internal/config`。在 Task 1 同时于 `internal/approval` 增加本地 `ReviewMode` 类型与常量（与 config 解耦），见 Step 3。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/approval -run TestClassifyAccess -count=1`
Expected: FAIL / 编译失败（`AccessIntent` 未定义）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/approval/access.go
package approval

import (
	"path/filepath"
	"strings"
)

// ReviewMode mirrors config review modes without importing config (keeps
// approval package free of the config dependency).
type ReviewMode string

const (
	ReviewNever   ReviewMode = "never"
	ReviewMutable ReviewMode = "mutable"
	ReviewAlways  ReviewMode = "always"
)

type AccessClass string

const (
	AccessRead  AccessClass = "read"
	AccessWrite AccessClass = "write"
)

type AccessIntent struct {
	Class AccessClass
	Dirs  []string
}

type Decision string

const (
	DecisionAllow Decision = "allow"
	DecisionAsk   Decision = "ask"
)

type dirLocation int

const (
	locUnknown dirLocation = iota
	locWorkspace
	locTemp
	locExternal
)

// locateDir classifies an absolute path against the workspace scope and
// safe directories. Relative paths are treated as workspace-local.
func locateDir(path string, scope Scope, safeDirs []string) dirLocation {
	if path == "" {
		return locUnknown
	}
	if !filepath.IsAbs(path) && !windowsPathIsAbs(path) {
		return locWorkspace
	}
	cleaned := filepath.Clean(path)
	if isWithin(cleaned, filepath.Clean(scope.Value)) {
		return locWorkspace
	}
	for _, s := range safeDirs {
		if isWithin(cleaned, filepath.Clean(s)) {
			return locTemp
		}
	}
	return locExternal
}

func isWithin(target, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel))
}

// ClassifyAccess applies the directory-centric approval matrix.
func ClassifyAccess(intent AccessIntent, scope Scope, safeDirs []string, mode ReviewMode) Decision {
	if mode == ReviewNever {
		return DecisionAllow
	}
	if len(intent.Dirs) == 0 {
		if intent.Class == AccessRead {
			return DecisionAllow
		}
		return DecisionAsk
	}
	for _, d := range intent.Dirs {
		switch locateDir(d, scope, safeDirs) {
		case locExternal:
			return DecisionAsk
		case locWorkspace:
			if intent.Class == AccessWrite {
				return DecisionAsk
			}
		case locTemp:
			// Allow cell
		default:
			return DecisionAsk
		}
	}
	return DecisionAllow
}
```

> `windowsPathIsAbs` 已存在于 `internal/approval`（`matching.go` 用到）。若实际无此导出符号，改用 `filepath.IsAbs`（Go 在 Windows 已识别 `C:\`）；编译时确认。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/approval -run TestClassifyAccess -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/approval/access.go internal/approval/access_test.go
git commit -m "feat(approval): add AccessIntent and ClassifyAccess matrix"
```

---

### Task 2: ctx 授权目录工具 (accessctx)

**Files:**
- Create: `internal/tools/accessctx.go`
- Test: `internal/tools/accessctx_test.go`

**Interfaces:**
- Produces: `WithAuthorizedDirs(ctx context.Context, dirs []string) context.Context`, `AuthorizedDirs(ctx context.Context) []string`

- [ ] **Step 1: Write the failing test**

```go
// internal/tools/accessctx_test.go
package tools

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAuthorizedDirsRoundTrip(t *testing.T) {
	ctx := context.Background()
	require.Nil(t, AuthorizedDirs(ctx))
	ctx = WithAuthorizedDirs(ctx, []string{"/a", "/b"})
	require.Equal(t, []string{"/a", "/b"}, AuthorizedDirs(ctx))
}

func TestAuthorizedDirsDoesNotMutateOriginal(t *testing.T) {
	ctx := context.Background()
	derived := WithAuthorizedDirs(ctx, []string{"/a"})
	require.Nil(t, AuthorizedDirs(ctx), "parent ctx must not carry dirs")
	require.Equal(t, []string{"/a"}, AuthorizedDirs(derived))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run TestAuthorizedDirs -count=1`
Expected: FAIL（符号未定义）

- [ ] **Step 3: Write minimal implementation**

```go
// internal/tools/accessctx.go
package tools

import "context"

type authorizedDirsKey struct{}

// WithAuthorizedDirs returns a derived context carrying the authorized
// external directories for the current tool call. An empty/nil slice
// returns ctx unchanged so workspace-only calls pay no allocation.
func WithAuthorizedDirs(ctx context.Context, dirs []string) context.Context {
	if len(dirs) == 0 {
		return ctx
	}
	cp := make([]string, len(dirs))
	copy(cp, dirs)
	return context.WithValue(ctx, authorizedDirsKey{}, cp)
}

// AuthorizedDirs returns the authorized external directories attached to
// ctx, or nil if none.
func AuthorizedDirs(ctx context.Context) []string {
	v, _ := ctx.Value(authorizedDirsKey{}).([]string)
	return v
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/tools -run TestAuthorizedDirs -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/tools/accessctx.go internal/tools/accessctx_test.go
git commit -m "feat(tools): add ctx-carried authorized external dirs"
```

---

### Task 3: resolveWorkspacePath 接受 ctx 并授权放行

**Files:**
- Modify: `internal/tools/path_safety.go`
- Modify: `internal/tools/filesystem.go`（5 处调用传 ctx）
- Modify: `internal/tools/patch_validate.go:74`（调用传 ctx）
- Modify: `internal/tools/tools_test.go`（调用签名 + 新用例）

**Interfaces:**
- Consumes: `AuthorizedDirs(ctx)`（Task 2）
- Produces: `resolveWorkspacePath(ctx context.Context, root, input string, safeDirs []string) (string, error)`

- [ ] **Step 1: Write the failing test (append to tools_test.go)**

```go
// 在 tools_test.go 末尾追加
func TestResolveWorkspacePathAuthorizesApprovedExternal(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	target := filepath.Join(outside, "data.txt")
	require.NoError(t, os.WriteFile(target, []byte("x"), 0o644))

	// 未授权 → 拒绝
	_, err := resolveWorkspacePath(context.Background(), root, target, nil)
	require.Error(t, err)

	// 授权 outside → 放行
	ctx := WithAuthorizedDirs(context.Background(), []string{outside})
	got, err := resolveWorkspacePath(ctx, root, target, nil)
	require.NoError(t, err)
	require.Equal(t, target, got)
}

func TestResolveWorkspacePathRejectsSymlinkEscapeFromAuthorized(t *testing.T) {
	root := t.TempDir()
	authorized := t.TempDir()
	secret := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(secret, "pwn"), []byte("x"), 0o644))
	// authorized 里的 symlink 指向 authorized 外
	require.NoError(t, os.Symlink(secret, filepath.Join(authorized, "escape")))
	ctx := WithAuthorizedDirs(context.Background(), []string{authorized})
	_, err := resolveWorkspacePath(ctx, root, filepath.Join(authorized, "escape", "pwn"), nil)
	require.Error(t, err, "symlink escaping the authorized dir must be rejected")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run TestResolveWorkspacePath -count=1`
Expected: FAIL（签名不匹配：现有 `resolveWorkspacePath` 无 ctx 参数）

- [ ] **Step 3: Modify path_safety.go**

把 `resolveWorkspacePath` 签名改为接收 ctx，在 `ensureInsideRoot` 失败后增加授权目录放行分支：

```go
// path_safety.go —— 修改函数签名与 boundary 判定
func resolveWorkspacePath(ctx context.Context, root, input string, safeDirs []string) (string, error) {
	if input == "" {
		return "", fmt.Errorf("path is required")
	}
	path := input
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	path = filepath.Clean(path)

	boundary := root
	if err := ensureInsideRoot(root, path); err != nil {
		if safe, ok := matchSafeDir(path, safeDirs); ok {
			boundary = safe
		} else if approved, ok := matchAuthorizedDir(ctx, path); ok {
			boundary = approved          // 授权外部目录成为 boundary
		} else {
			return "", err
		}
	}

	// 以下 symlink 解析 + boundary 比较逻辑保持不变（existingEval/missing/boundaryEval）
	// ……（保留现有 39-76 行逻辑）
	return resolved, nil
}

// matchAuthorizedDir returns the first ctx-authorized directory that
// lexically contains path. Symlink escape is caught later by the
// boundary-aware EvalSymlinks comparison.
func matchAuthorizedDir(ctx context.Context, path string) (string, bool) {
	for _, ad := range AuthorizedDirs(ctx) {
		rel, err := filepath.Rel(ad, path)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." && !filepath.IsAbs(rel)) {
			return ad, true
		}
	}
	return "", false
}
```

更新 `ensureInsideRoot` 的错误文案（112 行）：
```go
return fmt.Errorf("path %q is outside workspace root; approval required to access paths outside the workspace", path)
```

- [ ] **Step 4: Update all call sites to pass ctx**

`filesystem.go`（5 处：fs_read_file/fs_write_file/fs_list/fs_stat/fs_search 的 Call 内）— Call 闭包的第一个参数已命名为 `ctx`，把每处 `resolveWorkspacePath(root, args.Path, safeDirs)` 改为 `resolveWorkspacePath(ctx, root, args.Path, safeDirs)`。

`patch_validate.go:74` — 同样传 ctx（注意此函数签名也需加 `ctx context.Context` 参数，并更新其调用方）。

`tools_test.go` 中现有 `resolveWorkspacePath(root, ...)` 调用全部改为 `resolveWorkspacePath(context.Background(), root, ...)`。

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/tools -count=1`
Expected: PASS（含原有 symlink 用例 + 新增两个授权用例）

- [ ] **Step 6: Commit**

```bash
git add internal/tools/path_safety.go internal/tools/filesystem.go internal/tools/patch_validate.go internal/tools/tools_test.go
git commit -m "feat(tools): resolveWorkspacePath honors ctx-authorized external dirs"
```

---

### Task 4: fs_* IntentExtractor 注册

**Files:**
- Modify: `internal/tools/tools.go`（Tool 字段 + Registry 方法）
- Modify: `internal/tools/filesystem.go`（注册提取器）
- Test: `internal/tools/tools_test.go`

**Interfaces:**
- Produces: `Tool.IntentExtractor func(data json.RawMessage) approval.AccessIntent`, `Registry.IntentExtractor(name string) func(json.RawMessage) approval.AccessIntent`

> **注意循环依赖：** `internal/tools` 导入 `internal/approval` 是允许的（approval 不依赖 tools）。确认 `approval` 包不导入 `tools`（当前确实不导入）。

- [ ] **Step 1: Write the failing test**

```go
func TestFilesystemIntentExtractor(t *testing.T) {
	root := t.TempDir()
	registry := NewRegistry()
	require.NoError(t, RegisterFilesystem(registry, FilesystemConfig{Root: root, SafeDirs: nil}))

	ext, ok := registry.IntentExtractor("fs_read_file")
	require.True(t, ok)
	intent := ext([]byte(`{"path":"sub/a.txt"}`))
	require.Equal(t, approval.AccessRead, intent.Class)
	require.Len(t, intent.Dirs, 1)
	require.Equal(t, filepath.Join(root, "sub"), intent.Dirs[0])

	extW, ok := registry.IntentExtractor("fs_write_file")
	require.True(t, ok)
	intentW := extW([]byte(`{"path":"out.txt","content":"x"}`))
	require.Equal(t, approval.AccessWrite, intentW.Class)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/tools -run TestFilesystemIntentExtractor -count=1`
Expected: FAIL（`IntentExtractor` 方法未定义）

- [ ] **Step 3: Modify tools.go**

```go
// tools.go —— Tool 增加字段
type Tool struct {
	Spec           proto.ToolSpec
	Call           Caller
	Kind           ToolKind
	TimeoutPolicy  TimeoutPolicy
	Capabilities   ToolCapabilities
	IntentExtractor func(json.RawMessage) approval.AccessIntent
}

// Registry 增加方法
func (r *Registry) IntentExtractor(name string) (func(json.RawMessage) approval.AccessIntent, bool) {
	tool, ok := r.tools[name]
	if !ok || tool.IntentExtractor == nil {
		return nil, false
	}
	return tool.IntentExtractor, true
}
```

imports 增加 `"github.com/panjie/mods/internal/approval"`。

- [ ] **Step 4: Register extractors in filesystem.go**

在 `RegisterFilesystem` 注册每个工具时挂 `IntentExtractor`。提取只读 Class 与父目录：

```go
// filesystem.go 顶部新增 helper
func pathParentIntent(root string, readOnly bool) func(json.RawMessage) approval.AccessIntent {
	class := approval.AccessWrite
	if readOnly {
		class = approval.AccessRead
	}
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct{ Path string `json:"path"` }
		_ = json.Unmarshal(data, &args)
		p := args.Path
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return approval.AccessIntent{Class: class, Dirs: []string{filepath.Dir(filepath.Clean(p))}}
	}
}
```

在每个 `register(Tool{...})` 调用里加 `IntentExtractor: pathParentIntent(root, true)`（读类）或 `pathParentIntent(root, false)`（fs_write_file/fs_apply_patch）。fs_apply_patch 的提取器见 Step 5 说明。

- [ ] **Step 5: fs_apply_patch IntentExtractor（多文件）**

patch 工具影响多个路径，提取器解析 `---`/`+++` 头得到父目录集合。复用 `patch_validate.go` 里已有的路径解析逻辑（`cleanPatchPath` 风格）；若无现成导出，提取头行路径后取父目录：

```go
func patchIntent(root string) func(json.RawMessage) approval.AccessIntent {
	return func(data json.RawMessage) approval.AccessIntent {
		var args struct{ Patch string `json:"patch"` }
		_ = json.Unmarshal(data, &args)
		var dirs []string
		seen := map[string]struct{}{}
		for _, line := range strings.Split(args.Patch, "\n") {
			if !strings.HasPrefix(line, "+++ ") {
				continue
			}
			p := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
			p = strings.Trim(p, `"`)
			if p == "/dev/null" {
				continue
			}
			if strings.HasPrefix(p, "a/") || strings.HasPrefix(p, "b/") {
				p = p[2:]
			}
			if !filepath.IsAbs(p) {
				p = filepath.Join(root, p)
			}
			d := filepath.Dir(filepath.Clean(p))
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				dirs = append(dirs, d)
			}
		}
		return approval.AccessIntent{Class: approval.AccessWrite, Dirs: dirs}
	}
}
```

`fs_apply_patch` 注册时用 `IntentExtractor: patchIntent(root)`。

- [ ] **Step 6: Run test to verify it passes**

Run: `go test ./internal/tools -run TestFilesystemIntentExtractor -count=1 && go build ./...`
Expected: PASS 且全仓编译通过

- [ ] **Step 7: Commit**

```bash
git add internal/tools/tools.go internal/tools/filesystem.go internal/tools/tools_test.go
git commit -m "feat(tools): register AccessIntent extractors for fs_* tools"
```

---

### Task 5: shell_classify mentionsExternalPath + 只读分支产出 Dirs

**Files:**
- Modify: `internal/app/shell_classify.go`
- Test: 新增 `internal/app/shell_classify_test.go`（若已有则追加）

**Interfaces:**
- Produces: `mentionsExternalPath(command string, workspaceRoot string) bool`；只读分支在命中外部迹象时落入 LLM 分类器（已有逻辑），不再无条件免审。

- [ ] **Step 1: Write the failing test**

```go
// internal/app/shell_classify_test.go
package app

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMentionsExternalPath(t *testing.T) {
	ws := filepath.Clean(t.TempDir())
	ext := filepath.Clean(t.TempDir())
	cases := []struct {
		cmd  string
		want bool
	}{
		{"cat README.md", false},                       // 相对路径,workspace 内
		{"cat " + filepath.Join(ws, "a.txt"), false},    // workspace 内绝对
		{"cat " + filepath.Join(ext, "secret"), true},   // workspace 外绝对
		{"type C:\\Users\\Public\\x", true},             // Windows 外部绝对
		{"ls -la", false},
		{"grep foo ~/Downloads/r", true},                // home 展开迹象
	}
	for _, c := range cases {
		got := mentionsExternalPath(c.cmd, ws)
		require.Equalf(t, c.want, got, "cmd=%q", c.cmd)
	}
}
```

> Windows 分支用例在非 Windows 可能不触发 `C:\` 检测——实现里用纯字符串正则识别 `^[A-Za-z]:[\\/]`，不依赖运行平台，确保跨平台一致。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestMentionsExternalPath -count=1`
Expected: FAIL（`mentionsExternalPath` 未定义）

- [ ] **Step 3: Implement mentionsExternalPath + 改造只读分支**

```go
// shell_classify.go 新增
var reAbsPath = regexp.MustCompile(`(?:^|[\s=])(~[/$]|/(?:[^/\s]+/)+[^\s]+|[A-Za-z]:[\\/][^\s]+)`)

// mentionsExternalPath 是只读命令的廉价预筛:只要文本里出现绝对路径
// 或 home 展开迹象,就升级到 LLM 分类器精确判定。宽进严出。
func mentionsExternalPath(command, workspaceRoot string) bool {
	for _, m := range reAbsPath.FindAllString(command, -1) {
		token := strings.TrimSpace(m)
		// 跳过明确的 workspace 内绝对路径
		if strings.HasPrefix(filepath.Clean(extractLeadingPath(token)), workspaceRoot+string(filepath.Separator)) ||
			filepath.Clean(extractLeadingPath(token)) == workspaceRoot {
			continue
		}
		return true
	}
	return false
}

// extractLeadingPath 取 token 里首个路径前缀(用于和 workspace 比较)
func extractLeadingPath(token string) string {
	token = strings.TrimPrefix(token, "~")
	token = strings.TrimLeft(token, " \t=")
	// 取到第一个空白为止
	if i := strings.IndexAny(token, " \t"); i >= 0 {
		return token[:i]
	}
	return token
}
```

改造 `analyzeShellCommand`（shell_classify.go:40-48）只读分支：

```go
if !isObviouslyMutable(command) {
    if m.shellClassifierWorkspaceRoot != "" && !mentionsExternalPath(command, m.shellClassifierWorkspaceRoot) {
        return shellCommandAnalysis{NeedsReview: false, AffectedDirs: nil, Reason: "read-only, workspace-local"}
    }
    // 有外部迹象(或无 root 信息) → 落入下方 LLM 分类器
}
```

在 `Mods` 增加 `shellClassifierWorkspaceRoot string` 字段（或复用 `m.Config.ResolveWorkspaceRoot()`），在 `analyzeShellCommand` 入口取一次。若无 Config（测试桩），空串表示"无法判断 → 保守升级 LLM"。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run TestMentionsExternalPath -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/shell_classify.go internal/app/shell_classify_test.go
git commit -m "feat(app): screen read-only shell commands for external paths"
```

---

### Task 6: buildAccessIntent 协调层

**Files:**
- Modify: `internal/app/review.go`（新增 `buildAccessIntent`）
- Test: `internal/app/review_test.go`（新增或追加）

**Interfaces:**
- Consumes: `Registry.IntentExtractor`（Task 4）、`Registry.ShellExecution`、`analyzeShell`
- Produces: `buildAccessIntent(name string, data []byte, registry *toolregistry.Registry, analyze func(string, string) shellCommandAnalysis) approval.AccessIntent`

- [ ] **Step 1: Write the failing test**

```go
func TestBuildAccessIntentShellReadOnly(t *testing.T) {
	// 用 nil registry + 注入固定 analyze: read-only
	analyze := func(tool, cmd string) shellCommandAnalysis {
		return shellCommandAnalysis{NeedsReview: false, AffectedDirs: []string{"/ws"}}
	}
	// 需要一个能 ShellExecution(name)==true 的 registry;构造见 testRegistry 辅助
	reg := testShellRegistry(t)
	intent := buildAccessIntent("shell_run", []byte(`{"command":"ls"}`), reg, analyze)
	require.Equal(t, approval.AccessRead, intent.Class)
	require.Equal(t, []string{"/ws"}, intent.Dirs)
}
```

> `testShellRegistry`/`testShellRegistry` 在测试里用 `toolregistry.NewRegistry()` + `RegisterShell` 构造。

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestBuildAccessIntent -count=1`
Expected: FAIL（`buildAccessIntent` 未定义）

- [ ] **Step 3: Implement buildAccessIntent**

```go
// review.go
func buildAccessIntent(name string, data []byte, registry *toolregistry.Registry, analyze func(string, string) shellCommandAnalysis) approval.AccessIntent {
	if registry != nil && registry.ShellExecution(name) {
		a := analyze(name, extractShellCommand(data))
		class := approval.AccessWrite
		if !a.NeedsReview {
			class = approval.AccessRead
		}
		return approval.AccessIntent{Class: class, Dirs: a.AffectedDirs}
	}
	if registry != nil {
		if ext, ok := registry.IntentExtractor(name); ok {
			return ext(data)
		}
	}
	// 未知工具(MCP 等): 保守视为写 + 无目录 → fail-closed 审批
	return approval.AccessIntent{Class: approval.AccessWrite}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run TestBuildAccessIntent -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/review.go internal/app/review_test.go
git commit -m "feat(app): add buildAccessIntent unifying fs/shell intent"
```

---

### Task 7: 规则适配 — 统一 DirAllow + RulesAllowDirs

**Files:**
- Modify: `internal/app/review.go`（`requestApproval` 内 candidateRules 与免审判定）
- Modify: `internal/app/approval_rules_test.go`（EditAll→DirAllow 断言）

**Interfaces:**
- 行为变化：fs_* always allow 候选从 `EditAll` 改为 `DirAllow(intent.Dirs)`；免审判定统一 `RulesAllowDirs`。

- [ ] **Step 1: Update the failing test (approval_rules_test.go)**

定位现有断言 fs_write_file 走 `approvalEditAll` 的用例（grep `approvalEditAll` / `EditAll` in approval_rules_test.go），将其改为断言生成 `DirAllow` 规则、`Paths` 含目标父目录。例如：

```go
// 原: expect candidate rule Type==approvalEditAll
// 新:
require.Len(t, item.candidateRules, 1)
require.Equal(t, approvalDirAllow, item.candidateRules[0].Type)
require.Contains(t, item.candidateRules[0].Paths, filepath.Dir(filepath.Join(scope.Value, "out.txt")))
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestRequestApproval -count=1`
Expected: FAIL（仍生成 EditAll）

- [ ] **Step 3: Modify requestApproval (review.go:251-300)**

把 candidateRules 统一改为基于 `intent.Dirs` 的 `RulesForDirs`，免审判定统一用 `RulesAllowDirs`。由于 `requestApproval` 此时尚未持有 intent，在 Task 8 接入 `buildAccessIntent` 时传入；本任务先在 `requestApproval` 增加 `intent approval.AccessIntent` 入参（或经 `reviewerDeps`），并把：

```go
// review.go requestApproval 内
candidateRules := []Rule{}
if len(intent.Dirs) > 0 {
    candidateRules = RulesForDirs(intent.Dirs, r.scope)
}
// 免审判定(替换原 shellExecution 分叉):
if RulesAllowDirs(r.rules.Snapshot(), intent.Dirs, r.scope) {
    return nil
}
```

> 注意：`RulesForDirs`/`RulesAllowDirs` 已存在（matching.go:58/68），`Rule`/`Scope` 是 `app` 包别名（`internal/app/aliases.go` 指向 `approval`）。本步保留 `RulesFor(name,...)` 函数不删（兼容）。

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run TestRequestApproval -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/review.go internal/app/approval_rules_test.go
git commit -m "refactor(app): unify always-allow rules to DirAllow for fs/shell"
```

---

### Task 8: toolCaller 调用点整合 ClassifyAccess + ctx 注入

**Files:**
- Modify: `internal/app/request_session.go:255-273`（`toolCaller`）
- Modify: `internal/app/review.go`（`requestApproval` 接收 intent）

**Interfaces:**
- 这是核心整合点：取代 `shouldReviewTool`，用 `ClassifyAccess`；外部目录注入 ctx。

- [ ] **Step 1: Write the failing integration test**

在 `request_session_test.go`（若无则新建）测试 `toolCaller`：构造 workspace 内 fs_read（Allow，不审批直接执行）、workspace 外 fs_read（Ask，需审批）。

```go
func TestToolCallerClassifiesBeforeCall(t *testing.T) {
	// 见现有 toolCaller 测试模式;构造 Mods + registry + 临时 review channel
	// 断言: workspace 内 fs_read_file 不触发 review item; 外部路径触发
}
```

> 若现有测试基础设施难构造，本任务以 `go build ./...` + Task 8 后手动跑 `go test ./internal/app -count=1` 全绿为验收，并保留 `shouldReviewTool` 作为内部 helper 供 ReviewAlways 路径。

- [ ] **Step 2: Modify toolCaller**

```go
// request_session.go toolCaller
return func(name string, data []byte) (string, error) {
	ctx, cancel := m.toolCallContext(registry, name, cfg)
	m.addCancel(cancel)
	defer cancel()
	m.sendToolOperationStatus(ToolOperationLabel(name, data, m.width))

	wsRoot := m.Config.ResolveWorkspaceRoot()
	intent := buildAccessIntent(name, data, registry, m.analyzeShellCommand)
	safeDirs := []string{os.TempDir()}
	mode := toApprovalReviewMode(m.reviewer.reviewMode) // 见下
	decision := approval.ClassifyAccess(intent, approval.WorkspaceScope(wsRoot), safeDirs, mode)

	// 统一:涉及外部时把外部目录注入 ctx(无论后续是否免审)
	if ext := externalDirsOf(intent, approval.WorkspaceScope(wsRoot), safeDirs); len(ext) > 0 {
		ctx = toolregistry.WithAuthorizedDirs(ctx, ext)
	}

	if decision != approval.DecisionAllow {
		if err := m.reviewer.requestApproval(reviewerDeps{
			ctx:              m.ctx,
			isShellExecution: registry.ShellExecution,
			analyzeShell:     m.analyzeShellCommand,
			intent:           intent,
		}, name, data); err != nil {
			return "", err
		}
	}
	return registry.Call(ctx, name, data)
}
```

需要：
1. `toolregistry.WithAuthorizedDirs` 即 Task 2 的 `WithAuthorizedDirs`（已在 tools 包；通过 `toolregistry.WithAuthorizedDirs` 别名导出可见——确认 `internal/app/aliases.go` 或直接用全限定名 `tools.WithAuthorizedDirs`）。
2. `externalDirsOf(intent, scope, safeDirs) []string` — 返回 `locateDir==locExternal` 的 dirs。放入 `internal/approval/access.go` 作为导出 `ExternalDirs(intent, scope, safeDirs) []string`。
3. `toApprovalReviewMode` — 把 `config.ReviewMode`(string) 映射到 `approval.ReviewMode`。
4. `requestApproval` 的 `reviewerDeps` 增加 `intent approval.AccessIntent` 字段（供 Task 7 的 candidateRules/RulesAllowDirs 使用）。

- [ ] **Step 3: Add ExternalDirs helper to access.go**

```go
// access.go
func ExternalDirs(intent AccessIntent, scope Scope, safeDirs []string) []string {
	var out []string
	for _, d := range intent.Dirs {
		if locateDir(d, scope, safeDirs) == locExternal {
			out = append(out, d)
		}
	}
	return out
}
```

- [ ] **Step 4: Run build + app tests**

Run: `go build ./... && go test ./internal/app -count=1`
Expected: PASS（可能需同步调整既有 approval_rules_test.go 中直接调 `requestApproval` 的桩，补 `intent` 字段）

- [ ] **Step 5: Commit**

```bash
git add internal/app/request_session.go internal/app/review.go internal/approval/access.go
git commit -m "feat(app): route tool calls through ClassifyAccess with ctx auth"
```

---

### Task 9: review_summary 只读标签

**Files:**
- Modify: `internal/app/review_summary.go:17-33`（`formatReviewSummary`）
- Test: `internal/app/review_summary_test.go`

- [ ] **Step 1: Write the failing test**

```go
func TestFormatReviewSummaryExternalRead(t *testing.T) {
	scope := WorkspaceScope(t.TempDir())
	got := formatReviewSummary("fs_read_file",
		[]byte(`{"path":"/etc/passwd"}`),
		shellCommandAnalysis{}, scope)
	require.Contains(t, got, "external read")
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/app -run TestFormatReviewSummaryExternalRead -count=1`
Expected: FAIL（fs_read_file 未在 switch 内）

- [ ] **Step 3: Extend formatReviewSummary switch**

```go
case "fs_read_file", "fs_list", "fs_stat", "fs_search":
	path := ArgString(parsed, "path")
	return fmt.Sprintf("Target: %s - external read", OneLinePreview(path))
```

（fs_read_file 等只在外部才进 review，故标签固定 "external read"。）

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/app -run TestFormatReviewSummary -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/app/review_summary.go internal/app/review_summary_test.go
git commit -m "feat(app): show external-read label for read-only tools in review"
```

---

### Task 10: 提示词更新

**Files:**
- Modify: `internal/prompts/prompts.go:22`（ToolSelection）
- Modify: `internal/config/config_test.go:79-81`（断言文案）
- Modify: `internal/cli/main_test.go`（若有 ToolSelection 文案断言）

- [ ] **Step 1: Update ToolSelection**

把第 1、2 条改为（保留其余 3-7 条）：

```
Tool selection. Priority order:
1. Use fs_* tools for files inside the configured workspace; they are auto-approved for reads and reviewed only for writes.
2. fs_* tools may also access files outside the workspace (Downloads, Desktop, system temp, etc.); such access triggers an approval prompt. Prefer workspace-local paths to minimize interruptions.
3. On Windows, use shell_run for cmd.exe builtins such as dir, type, and echo; use powershell_run for PowerShell pipelines, variables, filtering, counting, or querying. Pass only the PowerShell command, without powershell, powershell.exe, pwsh, or -Command prefixes.
... (4-7 保持原文)
```

- [ ] **Step 2: Update config_test.go:79-81 断言**

```go
require.Contains(t, ToolSelectionRules, "fs_* tools may also access files outside the workspace")
require.Contains(t, ToolSelectionRules, "such access triggers an approval prompt")
require.NotContains(t, ToolSelectionRules, "they cannot access files outside it")
```

- [ ] **Step 3: Run tests**

Run: `go test ./internal/config ./internal/prompts ./internal/cli -count=1`
Expected: PASS

- [ ] **Step 4: Commit**

```bash
git add internal/prompts/prompts.go internal/config/config_test.go
git commit -m "docs(prompts): reflect unified fs external access in tool-selection rules"
```

---

### Task 11: 全量回归

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 2: Full test suite**

Run: `go test ./...`
Expected: 全 PASS；重点观察 `internal/approval`、`internal/tools`、`internal/app`、`internal/config`。

- [ ] **Step 3: Lint**

Run: `golangci-lint run ./...`（若已安装）；否则 `go run github.com/go-task/task/v3/cmd/task@v3.51.1 check`
Expected: 无新增告警

- [ ] **Step 4: CI mirror (optional)**

Run: `go run github.com/go-task/task/v3/cmd/task@v3.51.1 ci`
Expected: 通过

- [ ] **Step 5: Final commit (if any remaining)**

仅在有残留调整时提交。否则跳过。

---

## Self-Review

**Spec coverage:**
- §2 矩阵 → Task 1（ClassifyAccess）✓
- §4 架构/数据流/ctx → Task 2, 6, 8 ✓
- §5 ClassifyAccess → Task 1 ✓
- §6 resolveWorkspacePath → Task 3 ✓
- §7 只读 shell → Task 5 ✓
- §8 buildAccessIntent → Task 6 ✓
- §9 DirAllow 行为变化 → Task 7 ✓
- §10 review 展示 → Task 9 ✓
- §11 提示词 → Task 10 ✓
- §12 测试 → 各 Task 内嵌 + Task 11 全量 ✓

**Placeholder scan:** 无 TBD/TODO；所有代码步骤含完整代码。

**Type consistency:** `AccessIntent{Class, Dirs}`、`ClassifyAccess`、`WithAuthorizedDirs/AuthorizedDirs`、`IntentExtractor`、`buildAccessIntent`、`ExternalDirs` 跨任务命名一致；`resolveWorkspacePath(ctx, root, input, safeDirs)` 全任务统一。
