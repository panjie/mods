# 提示词意图授权（Prompt-Intent Approval）— 设计

**日期：** 2026-08-27
**状态：** Approved（默认开启；两意图标签 + 写范围分类器）
**范围：** `internal/approval`（意图/写范围枚举）、`internal/app`（两个分类器 + 审批门）、`internal/prompts`（两个分类器提示词）、`internal/config`（开关 + 提示词覆盖）

## 1 背景与问题

审批子系统（`2026-07-02-unified-directory-approval-design`）的核心原则是：**授权只来自可验证的工具调用本地文件系统事实，永远不来自语义声明**。因此当用户说"提交并推送本地修改"时，`git add -A` 仍会被拦下——它是 write 效应且没有具体目标目录，按矩阵 fail-closed 询问（"Modify an unknown target"）。

本设计引入**基于语义声明的能力授权**：把用户提示词映射到封闭枚举的意图标签，标签解析为两个固定能力——**写 workspace** 与**全局读**。远程读写不在审批范围之内（不设审批）。目标是消除用户已明确要求的操作的重复询问，同时保持"外部写 / unknown 效应 / 动态写目标永远询问"的硬边界。

## 2 标签与授权

**意图标签（提示词 → 标签，每轮一次）**

| 标签 | 语义 | 放行规则 |
|---|---|---|
| `workspace-edit` | 改 workspace 内文件（编辑/提交/构建/安装/格式化…） | 写 + 目录非空且 ⊆ workspace；空目录写 → 写范围分类器判 `workspace` |
| `global-read` | 全局读 | 读 + 任意目录（含 external 与动态目标） |

**写范围分类器（空目录写命令 → 多选，一次 LLM 调用）**

```
{"scopes":["workspace","external","unknown"]}   // 任意子集，空 = 无本地写
```

- `workspace`：本地写仅在 workspace 内（.git/node_modules/构建产物/cwd 相对产物）
- `external`：本地写到 workspace 之外
- `unknown`：无法判断
- 空数组：**无本地写**（纯远端/网络操作）→ **不设审批，直接放行**

**硬边界（永远询问）**：外部本地写、unknown 效应、动态写目标。

## 3 关键决策

| 目标 | 决策 |
|---|---|
| 默认状态 | 开启（`prompt-intent: true`；可显式关闭，关闭时零额外 LLM 调用） |
| 意图标签 | `workspace-edit`、`global-read`（封闭枚举，未知标签丢弃） |
| 远程读写 | 不设审批（无 `remote` 标签、无 `remote` 范围；空 scopes = 无本地写 = 放行） |
| 空目录写 | 写范围分类器判定 workspace/external/unknown/无本地写，fail-closed |
| 分类时机 | 惰性：意图分类仅在矩阵会 ask 时跑；写范围分类仅在空目录写且意图非空时跑 |
| 生效模式 | 仅 `review-mode: auto` |
| 可见性 | `approvalTrace.Source = "prompt intent"` + 状态行后缀 `· intent-allowed (<labels>)` |
| 配置 | `prompt-intent` bool；`prompts.prompt-intent-classifier`、`prompts.write-scope-classifier` 可覆盖 |

## 4 架构

```
用户提示词 ──classifyPromptIntent（每轮一次，5s 超时，LRU，失败→空）──► {workspace-edit, global-read}
                                                              │
toolCaller（request_session.go）                              │
  ├─ assessCommand → assessment（effect + KnownDirs）          │
  ├─ 写 + 空目录 + 意图非空：                                   │
  │     classifyWriteScope（5s 超时，LRU，失败→unknown）       │
  │      → {workspace|external|unknown|空}                     │
  └─ requestApproval（纯函数 gate，脱离 Mods）
        ① saved rule  ② 矩阵 auto  ③ prompt-intent 门  ④ 交互询问
```

## 5 审批门（promptIntentGrants）必要条件

- `review-mode == auto` 且 `promptIntents` 非空
- shell 执行时 `assessment.Effect != unknown`
- `intent.HasUnresolvedPaths() && DominantClass() == write` → 拒绝（动态写）
- 逐访问组检查：
  - read 组 → 需 `global-read`
  - write 组（目录非空）→ 需 `workspace-edit`，且目录全 ⊆ workspace
  - write 组（目录为空）→ 依 `writeScopes`：
    - 含 `external`/`unknown` → 询问
    - 含 `workspace` → 需 `workspace-edit`
    - 空（无本地写）→ 放行，标签 `remote`
    - `writeScopes == nil`（分类器未跑）→ 询问
- 返回覆盖本次调用的标签，作为 trace/状态行展示

## 6 对既有设计原则的有意识偏离

这是对 `2026-07-02` 原则（"授权只来自可验证事实"）的一次**有意识、受约束**的偏离，理由：

1. 语义授权被限制在两个固定能力内，标签无法表达"任意操作"。
2. 每个能力的放行仍须通过目录矩阵约束（写 ⊆ workspace）、效应证明（非 unknown）、动态目标拒绝；语义只是把"ask"降级为"allow"的**前置条件之一**。
3. 默认开启，但可通过配置或环境变量显式关闭。

`2026-08-07-command-reviewability-design`（"LLM 语义推断"列为 non-goal）与 todo 工具设计（"计划式审批门"列为 non-goal）的结论在本设计中保持：本设计不做自由语义推断，也不做一次性计划审批，仅做封闭枚举下的确定性放行。
