# 提示词意图授权（Prompt-Intent Approval）— 设计

**日期：** 2026-08-27
**状态：** Approved（默认关闭；两标签能力模型）
**范围：** `internal/approval`（意图枚举）、`internal/app`（分类器 + 审批门 + 写范围确认）、`internal/prompts`（意图分类器提示词）、`internal/config`（开关）

## 1 背景与问题

审批子系统（`2026-07-02-unified-directory-approval-design`）的核心原则是：**授权只来自可验证的工具调用本地文件系统事实，永远不来自语义声明**。因此当用户说"提交并推送本地修改"时，`git add -A` 仍会被拦下——它是 write 效应且没有具体目标目录，按矩阵 fail-closed 询问（"Modify an unknown target"）。

本设计引入一个**基于语义声明的能力授权**：把用户提示词映射到封闭枚举的意图标签，标签解析为两个固定能力——**写 workspace** 与**全局读**。目标是消除用户已明确要求的操作的重复询问，同时保持"外部写 / unknown 效应 / 动态写目标永远询问"的硬边界。

## 2 标签与授权矩阵

| 标签 | 语义 | 放行规则 |
|---|---|---|
| `workspace-edit` | 提示词表明要改动 workspace 内文件（编辑、提交、构建、安装、格式化…） | 写操作，且所有已知目录非空、⊆ workspace |
| `global-read` | 提示词表明要全局读文件系统（读 workspace 之外的配置/系统信息） | 读操作，任意目录（含 external 与动态目标） |

**硬边界（两标签都不覆盖，永远询问）**：外部写、unknown 效应、动态写目标。

## 3 关键决策

| 目标 | 决策 |
|---|---|
| 默认状态 | 关闭（`prompt-intent: false`，opt-in；关闭时零行为变化、零额外 LLM 调用） |
| 标签 | `workspace-edit`、`global-read`（封闭枚举，未知标签丢弃） |
| 空目录写 | 交给 AI 判断（复用 shell 分类器），判断为 workspace 内才放行，判断不出 fail-closed |
| 分类时机 | 惰性：仅当访问矩阵会 ask 时才解析意图（只读轮次零成本） |
| 生效模式 | 仅 `review-mode: auto`；`always` 保持"每次都问"，`never` 早已全放行 |
| 可见性 | `approvalTrace.Source = "prompt intent"` + 状态行后缀 `· intent-allowed (<tag>)` |
| 配置 | `prompt-intent` bool（YAML/`MODS_PROMPT_INTENT`）；`prompts.prompt-intent-classifier` 可覆盖意图分类提示词 |

## 4 架构

```
用户提示词 ──classifyPromptIntent（每轮一次，5s 超时，LRU 缓存，失败→空）──► {workspace-edit, global-read}
                                                              │
toolCaller（request_session.go）                              │
  ├─ assessCommand → assessment（effect + KnownDirs）          │
  ├─ 写 + 空目录 + workspace-edit 命中：                       │
  │     confirmWorkspaceWrite（复用 shell 分类器判断是否 workspace 内）
  │     命中 → assessment.KnownDirs = [workspace]              │
  └─ requestApproval（纯函数 gate，脱离 Mods）
        ① saved rule  ② 矩阵 auto  ③ prompt-intent 门  ④ 交互询问
```

## 5 审批门（promptIntentGrants）必要条件

- `review-mode == auto` 且 `promptIntents` 非空
- shell 执行时 `assessment.Effect != unknown`
- `intent.HasUnresolvedPaths() && DominantClass() == write` → 拒绝（动态写）
- 逐访问组检查：
  - read 组 → 需 `global-read`（任意目录，含动态）
  - write 组 → 需 `workspace-edit`，且目录非空、全部 ⊆ workspace
- 返回覆盖本次调用的标签，作为 trace/状态行展示

## 6 对既有设计原则的有意识偏离

这是对 `2026-07-02` 原则（"授权只来自可验证事实"）的一次**有意识、受约束**的偏离，理由：

1. 语义授权被限制在两个固定能力内，标签无法表达"任意操作"。
2. 每个能力的放行仍须通过目录矩阵约束（写 ⊆ workspace）、效应证明（非 unknown）、动态目标拒绝；语义只是把"ask"降级为"allow"的**前置条件之一**。
3. 默认关闭，opt-in。

`2026-08-07-command-reviewability-design`（"LLM 语义推断"列为 non-goal）与 todo 工具设计（"计划式审批门"列为 non-goal）的结论在本设计中保持：本设计不做自由语义推断，也不做一次性计划审批，仅做封闭枚举下的确定性放行。
