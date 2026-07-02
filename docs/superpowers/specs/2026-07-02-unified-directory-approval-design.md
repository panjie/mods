# 统一目录审批设计 (Unified Directory-Centric Approval)

- 状态：已确认（§1–§4 逐节通过）
- 日期：2026-07-02
- 范围：`internal/tools`、`internal/app`、`internal/approval`、`internal/prompts`

## 1. 背景与问题

mods 当前对"工具访问文件系统"存在两套粒度不一致的机制：

- **fs_\* 工具**（`internal/tools/path_safety.go`）：以**路径**为核心，`resolveWorkspacePath` 硬拒绝 workspace 外路径（含只读）。不区分读写。
- **shell 工具**（`internal/app/review.go` + `shell_classify.go`）：以**命令**为核心，通过 `affected_dirs` 间接做目录维度。区分读写（只读免审），但不区分内外（只读命令无论路径都免审）。

这导致"只读访问 workspace 外"落在两者空白带，行为割裂。本设计将两者统一为一个**以目录为核心**的审批矩阵。

## 2. 目标矩阵

默认 `ReviewMode = mutable` 下：

| Class ＼ 位置 | workspace 内 | temp dir (safeDir) | 外部 |
|---|---|---|---|
| **读** | Allow | Allow | **Ask** |
| **写** | **Ask** | Allow | **Ask** |

- `ReviewNever`：全局 Allow（覆盖矩阵）。
- `ReviewAlways`：强制走审批（覆盖矩阵；仍只对 mutable 工具生效，非 mutable 内部工具不被拖入）。
- temp dir（`os.TempDir()`）保留为免审安全区，矩阵 Allow 格。

## 3. 关键决策（已确认）

1. **审批通过 = 真正放行**：fs_\* 审批通过后能真正访问 workspace 外路径。`resolveWorkspacePath` 的"硬拒绝外部"改造为"授权目录内放行"。
2. **temp dir 保留免审安全区**：作为矩阵显式例外，维持 `SafeWorkspaceTemplate` 提示词承诺与中间文件工作流。
3. **只读 shell 也分析目录**：本地正则优先，拿不准时调 LLM 分类器；命中外部则审批。矩阵对 shell 完全成立。
4. **继承范围 = conversation 级**：被批准目录（`DirAllow` 规则）随对话落盘 `approval_rules` 表，`--continue` 恢复，新对话不继承。**不**升级为全局跨对话。
5. **架构方案 A**：引入统一 `AccessIntent` 抽象，review 层单一矩阵决策。

## 4. 架构（§1）

### 4.1 核心抽象 `AccessIntent`

归属 `internal/approval`（纯逻辑，无 LLM 依赖，可独立单测）：

```go
type AccessClass string
const ( AccessRead AccessClass = "read"; AccessWrite AccessClass = "write")

type AccessIntent struct {
    Class AccessClass   // fs_*: 来自 ReadOnly 能力; shell: !NeedsReview→read
    Dirs  []string      // 绝对路径; fs_*: path 父目录; shell: affected_dirs
}
```

### 4.2 模块归属（规避 tools ↛ app 循环依赖）

| 职责 | 归属 |
|---|---|
| `AccessIntent` 类型 + `ClassifyAccess` 矩阵 | `internal/approval` |
| fs_\* 静态意图提取（path→Dirs, ReadOnly→Class） | `internal/tools`（`Tool.IntentExtractor`） |
| shell 动态意图提取（`analyzeShell`） | `internal/app`（复用） |
| 协调 `buildAccessIntent` | `internal/app`（`review.go`） |

### 4.3 数据流（替换 `request_session.go:262-272`）

```
工具调用 (name, data)
 ├─① intent = buildAccessIntent(name, data, registry)
 ├─② ctx 注入: 若 intent 含外部目录 → ctx = WithAuthorizedDirs(ctx, extDirs)
 ├─③ decision = ClassifyAccess(intent, scope, safeDirs, mode)
 ├─ Allow → registry.Call(ctx, data)
 └─ Ask:
      if savedDirAllow命中(intent.Dirs) || (mode==Always && !mutable) → Call(ctx)
      else if !IsInputTTY() → error (fail)
      else → review:
            once  → Call(ctx)            // ctx 已授权本次
            always→ DirAllow(intent.Dirs) 入 reviewer.rules → Call(ctx)
            deny  → error
```

### 4.4 审批信息传递（ctx）

`shouldReviewTool`（仅看 `Mutable`）被 `buildAccessIntent` + `ClassifyAccess` 取代。

**关键**：免审（saved DirAllow 命中）≠ 不授权，仅跳过 review UI。凡涉及外部的 Call，统一在 Call 前把 `intent` 的外部目录经 `context.Value` 注入 ctx（`WithAuthorizedDirs` / `AuthorizedDirs`），`resolveWorkspacePath` 取出放行。这是审批层（Call 前）与路径放行（Call 内）解耦的唯一通道，无需改 `Call` 签名（ctx 已是现成参数）。

## 5. `ClassifyAccess` 矩阵（§2）

职责边界：`ClassifyAccess` 只做纯矩阵判定，**不查 saved rules**（rules 查询在 `requestApproval` 内，避免污染矩阵逻辑）。

```go
func ClassifyAccess(intent AccessIntent, scope Scope, safeDirs []string, mode ReviewMode) Decision {
    if mode == ReviewNever { return Allow }
    if len(intent.Dirs) == 0 {
        return intent.Class == AccessRead ? Allow : Ask   // 兜底:读宽容,写保守
    }
    for _, d := range intent.Dirs {
        switch locate(d, scope, safeDirs) {              // workspaceIn/temp/external
        case external:
            return Ask
        case workspaceIn:
            if intent.Class == AccessWrite { return Ask }
        case temp:
            // 矩阵 Allow 格
        }
    }
    return Allow
}
```

- `Decision`：Allow / Ask（无 Deny 格；拒绝来自用户选 deny 或非 TTY error）。
- saved `DirAllow` 命中 → 免审，位于 `requestApproval`（继承生效点）。
- `.env` 保护不引入（mods 现状无此特性），保持范围克制。

## 6. `resolveWorkspacePath` 改造（§3）

`internal/tools/path_safety.go`：签名加 `ctx`，保留规范化与 symlink 防护，外部从硬拒改为授权放行。

```go
func resolveWorkspacePath(ctx context.Context, root, input string, safeDirs []string) (string, error) {
    resolved := 规范化 + EvalSymlinks(input, root)        // 保留
    if inside(root) || inside(任一 safeDir) { return resolved }
    for _, ad := range AuthorizedDirs(ctx) {              // 新增
        if inside(EvalSymlinks(ad), resolved) { return resolved }
    }
    return "", errors.New("path outside workspace; requires approval")
}
```

- 授权目录自身 `EvalSymlinks` 后比较，防 symlink 逃逸（与 safeDir 同等对待）。
- 现有 symlink 逃逸测试须仍通过。
- 错误文案从 "use shell_run" 改为 "requires approval"。

## 7. 只读 shell 目录分析（§3）

`internal/app/shell_classify.go`：新增轻量启发式 `mentionsExternalPath(command, scope)`。

```go
if !isObviouslyMutable(command) {
    if !mentionsExternalPath(command, scope) {
        return {NeedsReview:false, AffectedDirs:nil, Reason:"read-only, workspace-local"}
    }
    // 有外部迹象 → 落入 LLM 分类器拿准确 affected_dirs
}
// LLM 分类器(mutable 命令 + 外部只读命令)
```

`mentionsExternalPath`：本地正则匹配 workspace 外绝对路径迹象（`~/`、`/etc`、`C:\Users` 等不在 scope.Value 之下）。**宽进严出**：怀疑即升级 LLM 确认；成本由现有 `shellClassifyCache`（LRU 256）兜底。

## 8. `buildAccessIntent`（§3）

```go
func buildAccessIntent(name string, data []byte, registry *toolregistry.Registry) AccessIntent {
    if registry.ShellExecution(name) {
        a := analyzeShell(name, extractShellCommand(data))
        class := AccessWrite; if !a.NeedsReview { class = AccessRead }
        return AccessIntent{Class: class, Dirs: a.AffectedDirs}
    }
    return registry.IntentExtractor(name)(data)
}
```

fs_\* 在 `RegisterFilesystem` 注册 `IntentExtractor`：
- `fs_read_file`/`fs_list`/`fs_stat`/`fs_search`：`Class=Read`，`Dirs=[absParent(path)]`
- `fs_write_file`/`fs_apply_patch`：`Class=Write`，`Dirs=[absParent(path)...]`

## 9. 规则适配（§4，含行为变化）

| 项 | 现状 | 新方案 |
|---|---|---|
| fs_\* always-allow 候选 | `EditAll`（全局所有编辑免审，matching.go:43-47） | **`DirAllow(intent.Dirs)`** |
| shell always-allow 候选 | `DirAllow(affected_dirs)` | 不变 |
| 免审判定 | fs_\* 查 `EditAll`；shell 查 `RulesAllowDirs` | **统一 `RulesAllowDirs(rules, intent.Dirs, scope)`** |

**行为变化**：`fs_write_file` 的 always allow 从"全局所有文件编辑免审"收紧为"按目录免审"。更安全、与 shell 对齐；便利性下降（不同目录需分别授权）。`EditAll`/`ToolAll` 类型保留以兼容历史 DB 记录，新会话不再生成。

## 10. review 展示（§4）

`review_summary.go`：新增只读工具进 review（外部读）的标签：
- fs_read_file/list/stat/search 外部读 → `external read: <预览>`
- fs_write_file/patch workspace 内 → `workspace mutation`；外部 → `external mutation`

## 11. 提示词（§4）

`prompts.go:22` ToolSelection 第 1、2 条改写：fs_\* 可访问 workspace 外（会触发审批），不再强制"外部必须用 shell"；优先 workspace 内以减少打断。`SafeWorkspaceTemplate` 不变。

## 12. 测试策略

| 层 | 测试 |
|---|---|
| `approval/access.go` | `ClassifyAccess` 矩阵全格(2×3) + ReviewNever/Always + Dirs 空 兜底 |
| `tools/path_safety_test` | 授权放行 / symlink 逃逸仍拒 / 未授权 → "requires approval" |
| `tools/filesystem` | fs_read_file 外部 once 批准能读；always 后续免审；拒绝 → error |
| `shell_classify` | `mentionsExternalPath` 各 case；只读外部升级 LLM |
| `review_summary` | fs_read_file 外部读标签 |
| 回归 | `approval_rules_test`(EditAll→DirAllow 调整断言) / `tools_test` / symlink 用例 |
| 集成 | request_session ctx 注入(免审也注入) / DirAllow 跨 fs+shell 命中 |

## 13. 非目标

- 不引入 `.env` 读保护。
- 不改变 MCP 工具审批（默认 Write + 未知 Dirs → fail-closed 审批，与现状一致）。
- 不升级外部目录授权为全局跨对话持久。
